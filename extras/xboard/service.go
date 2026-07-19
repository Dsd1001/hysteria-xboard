package xboard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/apernet/hysteria/core/v2/server"
)

type PanelClient interface {
	UsersClient
	TrafficSender
}

type ServiceConfig struct {
	Panel            PanelClient
	NodeID           string
	UserCachePath    string
	SpoolPath        string
	MaxUserStale     time.Duration
	UserPullInterval time.Duration
	FlushInterval    time.Duration
	BatchInterval    time.Duration
	ReportInterval   time.Duration
}

type InitializeResult struct {
	UsingCache bool
	SyncError  error
	CacheError error
}

// Service owns the Xboard control-plane components while exposing only the
// standard Hysteria authentication and traffic logging interfaces.
type Service struct {
	panel          PanelClient
	store          *UserStore
	syncer         *UserSyncer
	authenticator  *Authenticator
	collector      *TrafficCollector
	spool          *TrafficSpool
	reporter       *TrafficReporter
	now            func() time.Time
	pullInterval   time.Duration
	flushInterval  time.Duration
	batchInterval  time.Duration
	reportInterval time.Duration
	errors         chan error

	startMu sync.Mutex
	started bool
	closed  bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	closeOnce sync.Once
	closeErr  error
}

func NewService(config ServiceConfig) (*Service, error) {
	if config.Panel == nil {
		return nil, fmt.Errorf("Xboard panel client is required")
	}
	if config.NodeID == "" {
		return nil, fmt.Errorf("Xboard node ID is required")
	}
	store := NewUserStore()
	spool, err := OpenTrafficSpool(config.SpoolPath, config.NodeID)
	if err != nil {
		return nil, err
	}
	service := &Service{
		panel:          config.Panel,
		store:          store,
		authenticator:  NewAuthenticator(store, config.MaxUserStale),
		collector:      NewTrafficCollector(store),
		spool:          spool,
		now:            time.Now,
		pullInterval:   positiveDuration(config.UserPullInterval, time.Minute),
		flushInterval:  positiveDuration(config.FlushInterval, time.Second),
		batchInterval:  positiveDuration(config.BatchInterval, time.Minute),
		reportInterval: positiveDuration(config.ReportInterval, 5*time.Second),
		errors:         make(chan error, 64),
	}
	service.syncer = NewUserSyncer(config.Panel, store, config.UserCachePath, config.MaxUserStale)
	service.reporter = NewTrafficReporter(spool, config.Panel)
	return service, nil
}

func (s *Service) Initialize(ctx context.Context) (InitializeResult, error) {
	var result InitializeResult
	cacheLoaded := false
	if s.syncer.cachePath != "" {
		result.CacheError = s.syncer.LoadCache()
		cacheLoaded = result.CacheError == nil
	}

	_, syncErr := s.syncer.Sync(ctx)
	if syncErr == nil {
		return result, nil
	}
	result.SyncError = syncErr
	if cacheLoaded {
		result.UsingCache = true
		return result, nil
	}
	if result.CacheError != nil && !errors.Is(result.CacheError, os.ErrNotExist) {
		return result, errors.Join(syncErr, result.CacheError)
	}
	return result, syncErr
}

func (s *Service) Authenticator() server.Authenticator {
	return s.authenticator
}

func (s *Service) TrafficLogger() server.TrafficLogger {
	return s.collector
}

func (s *Service) Store() *UserStore {
	return s.store
}

func (s *Service) FlushTraffic() error {
	deltas := s.collector.Drain()
	if len(deltas) == 0 {
		return nil
	}
	if err := s.spool.AddPending(deltas); err != nil {
		s.collector.Merge(deltas)
		return err
	}
	return nil
}

func (s *Service) CreateTrafficBatch(createdAt time.Time) (*TrafficBatch, error) {
	return s.spool.CreateBatch(createdAt)
}

func (s *Service) ReportOldest(ctx context.Context) (bool, error) {
	reported, err := s.reporter.ReportOldest(ctx)
	if err != nil || !reported {
		return reported, err
	}
	if _, err := s.CreateTrafficBatch(s.now()); err != nil {
		return true, fmt.Errorf("create next Xboard traffic batch: %w", err)
	}
	return true, nil
}

func (s *Service) Start(parent context.Context) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if s.closed {
		return fmt.Errorf("Xboard service is closed")
	}
	if s.started {
		return fmt.Errorf("Xboard service is already started")
	}
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel
	s.started = true
	s.wg.Add(3)
	go s.runUserSync(ctx)
	go s.runTrafficPersistence(ctx)
	go s.runReporter(ctx)
	return nil
}

func (s *Service) Errors() <-chan error {
	return s.errors
}

func (s *Service) runUserSync(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(s.pullInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.syncer.Sync(ctx); err != nil && ctx.Err() == nil {
				s.emitError(fmt.Errorf("sync Xboard users: %w", err))
			}
		}
	}
}

func (s *Service) runTrafficPersistence(ctx context.Context) {
	defer s.wg.Done()
	flushTicker := time.NewTicker(s.flushInterval)
	batchTicker := time.NewTicker(s.batchInterval)
	defer flushTicker.Stop()
	defer batchTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-flushTicker.C:
			if err := s.FlushTraffic(); err != nil {
				s.emitError(fmt.Errorf("persist Xboard traffic: %w", err))
			}
		case <-batchTicker.C:
			if _, err := s.CreateTrafficBatch(s.now()); err != nil {
				s.emitError(fmt.Errorf("create Xboard traffic batch: %w", err))
			}
		}
	}
}

func (s *Service) runReporter(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(s.reportInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.ReportOldest(ctx); err != nil && ctx.Err() == nil {
				s.emitError(fmt.Errorf("report Xboard traffic: %w", err))
			}
		}
	}
}

func (s *Service) emitError(err error) {
	select {
	case s.errors <- err:
	default:
	}
}

func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.startMu.Lock()
		s.closed = true
		cancel := s.cancel
		s.startMu.Unlock()
		if cancel != nil {
			cancel()
		}
		s.wg.Wait()
		flushErr := s.FlushTraffic()
		_, batchErr := s.CreateTrafficBatch(s.now())
		closeErr := s.spool.Close()
		s.closeErr = errors.Join(flushErr, batchErr, closeErr)
		close(s.errors)
	})
	return s.closeErr
}

func positiveDuration(value, fallback time.Duration) time.Duration {
	if value <= 0 {
		return fallback
	}
	return value
}

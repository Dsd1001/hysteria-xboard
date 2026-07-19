package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/apernet/hysteria/extras/v2/xboard"
	"go.uber.org/zap"
)

const defaultXboardMaxUserStale = 6 * time.Hour

type xboardRuntime = xboard.Service

type serverConfigXboardUsers struct {
	CacheFile    string        `mapstructure:"cacheFile"`
	PullInterval time.Duration `mapstructure:"pullInterval"`
	MaxStale     time.Duration `mapstructure:"maxStale"`
}

type serverConfigXboardTraffic struct {
	SpoolFile      string        `mapstructure:"spoolFile"`
	FlushInterval  time.Duration `mapstructure:"flushInterval"`
	BatchInterval  time.Duration `mapstructure:"batchInterval"`
	ReportInterval time.Duration `mapstructure:"reportInterval"`
}

type serverConfigXboard struct {
	BaseURL   string                    `mapstructure:"baseURL"`
	Token     string                    `mapstructure:"token"`
	TokenFile string                    `mapstructure:"tokenFile"`
	NodeID    string                    `mapstructure:"nodeID"`
	AllowHTTP bool                      `mapstructure:"allowHTTP"`
	Timeout   time.Duration             `mapstructure:"timeout"`
	Users     serverConfigXboardUsers   `mapstructure:"users"`
	Traffic   serverConfigXboardTraffic `mapstructure:"traffic"`
}

func (c *serverConfig) prepareXboard(ctx context.Context) (xboard.InitializeResult, error) {
	if !strings.EqualFold(c.Auth.Type, "xboard") {
		return xboard.InitializeResult{}, nil
	}
	if c.Xboard.BaseURL == "" {
		return xboard.InitializeResult{}, configError{Field: "xboard.baseURL", Err: errors.New("empty Xboard base URL")}
	}
	if c.Xboard.NodeID == "" {
		return xboard.InitializeResult{}, configError{Field: "xboard.nodeID", Err: errors.New("empty Xboard node ID")}
	}
	if c.Xboard.Users.CacheFile == "" {
		return xboard.InitializeResult{}, configError{Field: "xboard.users.cacheFile", Err: errors.New("empty Xboard user cache file")}
	}
	if c.Xboard.Traffic.SpoolFile == "" {
		return xboard.InitializeResult{}, configError{Field: "xboard.traffic.spoolFile", Err: errors.New("empty Xboard traffic spool file")}
	}

	token, err := c.xboardToken()
	if err != nil {
		return xboard.InitializeResult{}, err
	}
	client, err := xboard.NewClient(xboard.Config{
		BaseURL:   c.Xboard.BaseURL,
		Token:     token,
		NodeID:    c.Xboard.NodeID,
		AllowHTTP: c.Xboard.AllowHTTP,
		Timeout:   c.Xboard.Timeout,
	})
	if err != nil {
		return xboard.InitializeResult{}, configError{Field: "xboard", Err: err}
	}
	maxStale := c.Xboard.Users.MaxStale
	if maxStale <= 0 {
		maxStale = defaultXboardMaxUserStale
	}
	service, err := xboard.NewService(xboard.ServiceConfig{
		Panel:            client,
		NodeID:           c.Xboard.NodeID,
		UserCachePath:    c.Xboard.Users.CacheFile,
		SpoolPath:        c.Xboard.Traffic.SpoolFile,
		MaxUserStale:     maxStale,
		UserPullInterval: c.Xboard.Users.PullInterval,
		FlushInterval:    c.Xboard.Traffic.FlushInterval,
		BatchInterval:    c.Xboard.Traffic.BatchInterval,
		ReportInterval:   c.Xboard.Traffic.ReportInterval,
	})
	if err != nil {
		return xboard.InitializeResult{}, configError{Field: "xboard", Err: err}
	}
	result, err := service.Initialize(ctx)
	if err != nil {
		_ = service.Close()
		return result, configError{Field: "xboard", Err: err}
	}
	c.xboardService = service
	return result, nil
}

func (c *serverConfig) xboardToken() (string, error) {
	if c.Xboard.Token != "" && c.Xboard.TokenFile != "" {
		return "", configError{Field: "xboard.token", Err: errors.New("token and tokenFile are mutually exclusive")}
	}
	if c.Xboard.Token != "" {
		return c.Xboard.Token, nil
	}
	if c.Xboard.TokenFile == "" {
		return "", configError{Field: "xboard.token", Err: errors.New("empty Xboard token")}
	}
	data, err := os.ReadFile(c.Xboard.TokenFile)
	if err != nil {
		return "", configError{Field: "xboard.tokenFile", Err: err}
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", configError{Field: "xboard.tokenFile", Err: errors.New("empty Xboard token file")}
	}
	return token, nil
}

func (c *serverConfig) startXboard(ctx context.Context) error {
	if c.xboardService == nil {
		return nil
	}
	if err := c.xboardService.Start(ctx); err != nil {
		return fmt.Errorf("start Xboard service: %w", err)
	}
	go func() {
		for err := range c.xboardService.Errors() {
			logger.Error("Xboard background operation failed", zap.Error(err))
		}
	}()
	return nil
}

func (c *serverConfig) closeXboard() error {
	if c.xboardService == nil {
		return nil
	}
	return c.xboardService.Close()
}

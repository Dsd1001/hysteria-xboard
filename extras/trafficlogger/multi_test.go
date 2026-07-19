package trafficlogger

import (
	"testing"

	"github.com/apernet/hysteria/core/v2/server"
	"github.com/stretchr/testify/assert"
)

type recordingTrafficLogger struct {
	allow        bool
	trafficCalls int
	onlineCalls  int
	traceCalls   int
	untraceCalls int
	lastID       string
	lastTx       uint64
	lastRx       uint64
	lastOnline   bool
	lastStream   server.HyStream
	lastStats    *server.StreamStats
}

func (l *recordingTrafficLogger) LogTraffic(id string, tx, rx uint64) bool {
	l.trafficCalls++
	l.lastID = id
	l.lastTx = tx
	l.lastRx = rx
	return l.allow
}

func (l *recordingTrafficLogger) LogOnlineState(id string, online bool) {
	l.onlineCalls++
	l.lastID = id
	l.lastOnline = online
}

func (l *recordingTrafficLogger) TraceStream(stream server.HyStream, stats *server.StreamStats) {
	l.traceCalls++
	l.lastStream = stream
	l.lastStats = stats
}

func (l *recordingTrafficLogger) UntraceStream(stream server.HyStream) {
	l.untraceCalls++
	l.lastStream = stream
}

func TestMultiTrafficLoggerCallsEveryLoggerAndCombinesDecision(t *testing.T) {
	allow := &recordingTrafficLogger{allow: true}
	deny := &recordingTrafficLogger{allow: false}
	last := &recordingTrafficLogger{allow: true}
	logger := NewMultiTrafficLogger(allow, nil, deny, last)

	ok := logger.LogTraffic("1001", 123, 456)

	assert.False(t, ok)
	for _, target := range []*recordingTrafficLogger{allow, deny, last} {
		assert.Equal(t, 1, target.trafficCalls)
		assert.Equal(t, "1001", target.lastID)
		assert.Equal(t, uint64(123), target.lastTx)
		assert.Equal(t, uint64(456), target.lastRx)
	}
}

func TestMultiTrafficLoggerReturnsTrueWhenNoLoggerDenies(t *testing.T) {
	logger := NewMultiTrafficLogger(
		&recordingTrafficLogger{allow: true},
		&recordingTrafficLogger{allow: true},
	)

	assert.True(t, logger.LogTraffic("1001", 1, 2))
	assert.True(t, NewMultiTrafficLogger().LogTraffic("1001", 1, 2))
}

func TestMultiTrafficLoggerFansOutAuxiliaryEvents(t *testing.T) {
	first := &recordingTrafficLogger{allow: true}
	second := &recordingTrafficLogger{allow: true}
	logger := NewMultiTrafficLogger(first, second)
	stats := &server.StreamStats{}

	logger.LogOnlineState("1002", true)
	logger.TraceStream(nil, stats)
	logger.UntraceStream(nil)

	for _, target := range []*recordingTrafficLogger{first, second} {
		assert.Equal(t, 1, target.onlineCalls)
		assert.Equal(t, "1002", target.lastID)
		assert.True(t, target.lastOnline)
		assert.Equal(t, 1, target.traceCalls)
		assert.Same(t, stats, target.lastStats)
		assert.Equal(t, 1, target.untraceCalls)
	}
}

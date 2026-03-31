package accesslog

import (
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

type AccessEntry struct {
	Timestamp   time.Time `json:"timestamp"`
	Method      string    `json:"method"`
	Path        string    `json:"path"`
	StatusCode  int       `json:"status_code"`
	Duration    int64     `json:"duration_ms"`
	ClientIP    string    `json:"client_ip"`
	UserAgent   string    `json:"user_agent"`
	BytesSent   int64     `json:"bytes_sent"`
	TargetNode  string    `json:"target_node,omitempty"`
	TargetURL   string    `json:"target_url,omitempty"`
}

type AccessLogger struct {
	file *os.File
	mu   sync.Mutex
	log  zerolog.Logger
}

func New(path string, log zerolog.Logger) (*AccessLogger, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	return &AccessLogger{
		file: file,
		log:  log,
	}, nil
}

func (a *AccessLogger) Log(entry AccessEntry) {
	a.mu.Lock()
	defer a.mu.Unlock()

	data, err := json.Marshal(entry)
	if err != nil {
		a.log.Error().Err(err).Msg("Failed to marshal access log entry")
		return
	}

	a.file.Write(data)
	a.file.Write([]byte("\n"))
}

func (a *AccessLogger) Close() {
	if a.file != nil {
		a.file.Close()
	}
}

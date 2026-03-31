package admin

import (
	"context"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"

	"novaairouter/internal/config"
	"novaairouter/internal/gossip"
	"novaairouter/internal/metrics"
	"novaairouter/internal/pool"
	"novaairouter/internal/registry"
)

//go:embed webui
var webuiFS embed.FS

// openWebUI opens a file from the embedded webui filesystem
func openWebUI(name string) (fs.FS, error) {
	return fs.Sub(webuiFS, "webui")
}

type AdminServer struct {
	config   *config.Config
	registry *registry.Registry
	metrics  *metrics.Metrics
	log      zerolog.Logger
	server   *http.Server
	gossip   *gossip.GossipServer
	poolMgr  *pool.PoolManager
}

func New(cfg *config.Config, reg *registry.Registry, m *metrics.Metrics, log zerolog.Logger, gs *gossip.GossipServer, poolMgr *pool.PoolManager) *AdminServer {
	return &AdminServer{
		config:   cfg,
		registry: reg,
		metrics:  m,
		log:      log,
		gossip:   gs,
		poolMgr:  poolMgr,
	}
}

func (s *AdminServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/endpoints", s.handleEndpoints)
	mux.HandleFunc("/v1/heartbeat", s.handleHeartbeat)
	mux.HandleFunc("/v1/nodes", s.handleNodesWithAuth)
	mux.HandleFunc("/v1/local", s.handleLocalWithAuth)
	mux.HandleFunc("/v1/global", s.handleGlobalWithAuth)
	mux.HandleFunc("/v1/node/", s.handleNodeByIDWithAuth)
	mux.HandleFunc("/metrics", s.handleMetricsWithAuth)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/v1/plugin/peers", s.handlePluginPeers)
	mux.HandleFunc("/v1/plugin/send", s.handlePluginSend)
	mux.HandleFunc("/v1/plugin/broadcast", s.handlePluginBroadcast)
	mux.HandleFunc("/v1/connect", s.handleConnect)

	// WebUI static file serving
	s.log.Info().Msg("Registering WebUI handlers")
	mux.HandleFunc("/v1/webui", s.handleWebUI)
	mux.HandleFunc("/v1/webui/", s.handleWebUI)
	s.log.Info().Msg("WebUI handlers registered")

	var basePort int
	fmt.Sscanf(s.config.ListenAddr, ":%d", &basePort)
	adminAddr := fmt.Sprintf("0.0.0.0:%d", basePort-1)

	s.server = &http.Server{
		Addr:    adminAddr,
		Handler: mux,
	}

	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			s.registry.CheckStaleEndpoints(s.config.HeartbeatTimeout)
			s.registry.CleanStaleRemoteNodes(s.config.HeartbeatTimeout*2, s.config.HeartbeatTimeout*3)
			// Update cluster metrics
			remoteNodes := s.registry.GetRemoteNodes()
			localEndpoints := s.registry.ListEndpoints()
			s.metrics.SetClusterNodes(float64(len(remoteNodes) + 1)) // +1 for local node
			s.metrics.SetClusterEndpoints(float64(len(localEndpoints)))
		}
	}()

	s.log.Info().Str("addr", s.server.Addr).Msg("Starting admin server")
	if s.config.TLSEnabled {
		return s.server.ListenAndServeTLS(s.config.TLSCertFile, s.config.TLSKeyFile)
	}
	return s.server.ListenAndServe()
}

func (s *AdminServer) Shutdown(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

// OnConfigChange 实现 ConfigListener 接口，处理配置变更
func (s *AdminServer) OnConfigChange(oldConfig, newConfig *config.Config) {
	s.log.Info().Msg("AdminServer: Received config change notification")

	// 如果监听地址改变了，需要重启服务
	if oldConfig.ListenAddr != newConfig.ListenAddr {
		s.log.Info().
			Str("old_addr", oldConfig.ListenAddr).
			Str("new_addr", newConfig.ListenAddr).
			Msg("Listen address changed, restarting admin server")

		// 计算新地址
		var basePort int
		fmt.Sscanf(newConfig.ListenAddr, ":%d", &basePort)
		adminAddr := fmt.Sprintf("0.0.0.0:%d", basePort-1)

		// 保存旧 server 引用用于关闭
		oldServer := s.server

		// 创建新 server，复用旧 handler
		s.server = &http.Server{
			Addr:    adminAddr,
			Handler: oldServer.Handler,
		}

		// 优雅关闭旧服务
		go func() {
			if oldServer != nil {
				oldServer.Shutdown(context.Background())
			}
		}()

		// 启动新服务
		go s.server.ListenAndServe()
	}

	// 更新其他配置
	s.config.HeartbeatTimeout = newConfig.HeartbeatTimeout

	s.log.Info().Msg("AdminServer: Config updated successfully")
}

func (s *AdminServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v1/endpoints" {
		s.handleEndpoints(w, r)
		return
	}
	if r.URL.Path == "/v1/heartbeat" {
		s.handleHeartbeat(w, r)
		return
	}
	if r.URL.Path == "/v1/nodes" {
		s.handleNodes(w, r)
		return
	}
	if r.URL.Path == "/metrics" {
		promhttp.Handler().ServeHTTP(w, r)
		return
	}
	http.NotFound(w, r)
}

// handleWebUI serves the webui static files
func (s *AdminServer) handleWebUI(w http.ResponseWriter, r *http.Request) {
	s.log.Info().Str("path", r.URL.Path).Msg("handleWebUI called")
	// Get the sub-filesystem for webui
	subFS, err := openWebUI(".")
	if err != nil {
		http.Error(w, "WebUI not available", http.StatusInternalServerError)
		return
	}

	// Strip the "/v1/webui" prefix from the path
	path := r.URL.Path
	if strings.HasPrefix(path, "/v1/webui") {
		path = strings.TrimPrefix(path, "/v1/webui")
	}
	if path == "" || path == "/" {
		path = "index.html"
	}
	// Remove leading slash for embed.FS compatibility
	path = strings.TrimPrefix(path, "/")

	// Open and serve the file
	file, err := subFS.Open(path)
	if err != nil {
		// If file not found, try index.html
		if path != "index.html" {
			file, err = subFS.Open("index.html")
		}
		if err != nil {
			http.NotFound(w, r)
			return
		}
	}
	defer file.Close()

	// Get file info for content type
	stat, err := file.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if stat.IsDir() {
		// Redirect directory requests to index.html
		http.Redirect(w, r, r.URL.Path+"/", http.StatusFound)
		return
	}

	// Set content type based on file extension
	contentType := getContentType(path)
	w.Header().Set("Content-Type", contentType)

	// Read file content into memory
	content, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "Failed to read file", http.StatusInternalServerError)
		return
	}

	// Set content length
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))

	// Write response
	w.WriteHeader(http.StatusOK)
	w.Write(content)
}

// getContentType returns the content type for a given file path
func getContentType(path string) string {
	switch {
	case strings.HasSuffix(path, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(path, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(path, ".js"):
		return "application/javascript; charset=utf-8"
	case strings.HasSuffix(path, ".json"):
		return "application/json; charset=utf-8"
	case strings.HasSuffix(path, ".png"):
		return "image/png"
	case strings.HasSuffix(path, ".jpg"), strings.HasSuffix(path, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(path, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(path, ".ico"):
		return "image/x-icon"
	case strings.HasSuffix(path, ".woff"):
		return "font/woff"
	case strings.HasSuffix(path, ".woff2"):
		return "font/woff2"
	default:
		return "application/octet-stream"
	}
}

package business

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"novaairouter/internal/config"
	"novaairouter/internal/models"
)

func (s *BusinessServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRequest)

	ln, err := net.Listen("tcp", s.config.ListenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", s.config.ListenAddr, err)
	}

	s.server = &http.Server{
		Addr:    s.config.ListenAddr,
		Handler: mux,
	}

	s.log.Info().Str("addr", s.server.Addr).Msg("Starting business server")
	if s.config.TLSEnabled {
		return s.server.ServeTLS(ln, s.config.TLSCertFile, s.config.TLSKeyFile)
	}
	return s.server.Serve(ln)
}

func (s *BusinessServer) Shutdown(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

// OnConfigChange 实现 ConfigListener 接口，处理配置变更
func (s *BusinessServer) OnConfigChange(oldConfig, newConfig *config.Config) {
	s.log.Info().Msg("BusinessServer: Received config change notification")

	// 如果监听地址改变了，需要重启服务
	if oldConfig.ListenAddr != newConfig.ListenAddr {
		s.log.Info().
			Str("old_addr", oldConfig.ListenAddr).
			Str("new_addr", newConfig.ListenAddr).
			Msg("Listen address changed, restarting business server")

		// 创建新的监听
		ln, err := net.Listen("tcp", newConfig.ListenAddr)
		if err != nil {
			s.log.Error().Err(err).Msg("Failed to create new listener")
			return
		}

		// 优雅关闭旧服务
		oldServer := s.server
		go func() {
			if oldServer != nil {
				oldServer.Shutdown(context.Background())
			}
		}()

		// 创建新服务
		mux := http.NewServeMux()
		mux.HandleFunc("/", s.handleRequest)
		s.server = &http.Server{
			Addr:    newConfig.ListenAddr,
			Handler: mux,
		}
		s.config.ListenAddr = newConfig.ListenAddr

		// 启动新服务
		go s.server.Serve(ln)
	}

	// 更新其他配置
	s.config.LogLevel = newConfig.LogLevel
	s.config.BackendTimeout = newConfig.BackendTimeout
	s.config.QueueTimeout = newConfig.QueueTimeout

	s.log.Info().Msg("BusinessServer: Config updated successfully")
}

func (s *BusinessServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handleRequest(w, r)
}

func (s *BusinessServer) handleRequest(w http.ResponseWriter, r *http.Request) {
	result := s.router.HandleRequest(w, r)
	if result == nil {
		return
	}

	switch v := result.(type) {
	case *models.LocalEndpoint:
		s.log.Info().Str("path", v.NodePath).Msg("Handling as LocalEndpoint")
		s.forwarder.HandleLocalRequest(w, r, r.URL.Path, v, time.Now())
	case *models.RemoteNode:
		s.log.Info().Str("node_id", v.NodeID).Str("address", v.Address).Str("node_path", v.NodePath).Msg("Got RemoteNode")
		if v.NodeID == s.config.NodeID && v.Address == "127.0.0.1" {
			s.log.Info().Str("node_path", v.NodePath).Msg("Local node detected, getting endpoint")
			if localEp, ok := s.registry.GetEndpoint(v.NodePath); ok {
				s.log.Info().Str("node_path", localEp.NodePath).Msg("Found local endpoint, calling HandleLocalRequest")
				s.forwarder.HandleLocalRequest(w, r, r.URL.Path, localEp, time.Now())
				return
			} else {
				s.log.Warn().Str("node_path", v.NodePath).Msg("Local endpoint not found!")
			}
		}
		s.forwarder.HandleForwardedRequest(w, r, r.URL.Path, v, time.Now())
	}
}

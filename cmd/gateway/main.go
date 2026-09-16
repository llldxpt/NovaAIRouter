package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"

	"novaairouter/internal/admin"
	"novaairouter/internal/business"
	"novaairouter/internal/config"
	"novaairouter/internal/gossip"
	syncsvc "novaairouter/internal/gossip/sync"
	"novaairouter/internal/logger"
	"novaairouter/internal/metrics"
	"novaairouter/internal/pool"
	"novaairouter/internal/registry"
)

const (
	forceFlagName = "force"
	forceFlagDesc = "Force run by killing processes using ports (15049-15052)"
)

type portInfo struct {
	port int
	name string
}

func checkPorts(ports []int, names []string) (available []int, unavailable []portInfo) {
	available = make([]int, 0, len(ports))
	unavailable = make([]portInfo, 0, len(ports))

	for i, port := range ports {
		name := "unknown"
		if i < len(names) {
			name = names[i]
		}

		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			unavailable = append(unavailable, portInfo{port: port, name: name})
		} else {
			ln.Close()
			available = append(available, port)
		}
	}
	return
}

func getPortList(ports []portInfo) string {
	result := ""
	for i, p := range ports {
		if i > 0 {
			result += ", "
		}
		result += strconv.Itoa(p.port)
	}
	return result
}

func killPort(port int) {
	for i := 0; i < 3; i++ {
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err == nil {
			ln.Close()
			return
		}

		cmd := exec.Command("cmd", "/C", fmt.Sprintf("for /f \"tokens=5\" %%a in ('netstat -ano ^| findstr :%d ^| findstr LISTENING') do taskkill /F /PID %%a", port))
		cmd.Run()
		time.Sleep(500 * time.Millisecond)

		ln, err = net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err == nil {
			ln.Close()
			return
		}
	}
}

var (
	version    = "v0.99"
	log        zerolog.Logger
	forceRun   bool
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "gateway",
		Short: "Decentralized AI Gateway Cluster",
		Long:  "A fully decentralized gateway cluster system for AI inference services",
		RunE:  runGateway,
	}

	config.AddFlags(rootCmd)
	rootCmd.Flags().BoolVarP(&forceRun, forceFlagName, "f", false, forceFlagDesc)
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runGateway(cmd *cobra.Command, _ []string) error {
	cfg, err := config.Load(cmd)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	log = logger.New(cfg.LogLevel)

	logFile, err := os.OpenFile(
		fmt.Sprintf("node_%s.log", cfg.NodeID),
		os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
		0644,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to create log file: %v\n", err)
	} else {
		defer logFile.Close()
		multiWriter := io.MultiWriter(os.Stdout, logFile)
		log = logger.New(cfg.LogLevel, multiWriter)
	}
	fmt.Fprintf(os.Stderr, "VERSION=%s NODE_ID=%s\n", version, cfg.NodeID)
	log.Info().Str("version", version).Str("node_id", cfg.NodeID).Msg("Starting gateway")

	bindPort := 0
	fmt.Sscanf(cfg.ListenAddr, ":%d", &bindPort)
	if bindPort <= 0 {
		bindPort = 15050
	}

	adminPort := bindPort - 1
	gossipPort := bindPort - 2
	udpPort := bindPort + 2

	ports := []int{bindPort, adminPort, gossipPort, udpPort}
	portNames := []string{"business", "admin", "gossip", "UDP discovery"}

	availablePorts, unavailablePorts := checkPorts(ports, portNames)

	if len(unavailablePorts) > 0 && !forceRun {
		fmt.Fprintf(os.Stderr, "\n")
		fmt.Fprintf(os.Stderr, "==============================================\n")
		fmt.Fprintf(os.Stderr, "  ERROR: Some ports are already in use\n")
		fmt.Fprintf(os.Stderr, "==============================================\n")
		fmt.Fprintf(os.Stderr, "\n")
		for _, p := range unavailablePorts {
			fmt.Fprintf(os.Stderr, "  ✗ Port %d (%s) is already in use\n", p.port, p.name)
		}
		fmt.Fprintf(os.Stderr, "\n")
		fmt.Fprintf(os.Stderr, "To force run and kill processes using these ports:\n")
		fmt.Fprintf(os.Stderr, "\n")
		fmt.Fprintf(os.Stderr, "  Option 1: Run with -f or --force flag:\n")
		fmt.Fprintf(os.Stderr, "    novaairouter.exe -f\n")
		fmt.Fprintf(os.Stderr, "    or\n")
		fmt.Fprintf(os.Stderr, "    novaairouter.exe --force\n")
		fmt.Fprintf(os.Stderr, "\n")
		fmt.Fprintf(os.Stderr, "  Option 2: Manually kill the processes:\n")
		fmt.Fprintf(os.Stderr, "    netstat -ano | findstr :%d\n", bindPort)
		fmt.Fprintf(os.Stderr, "    taskkill /F /PID <PID>\n")
		fmt.Fprintf(os.Stderr, "\n")
		fmt.Fprintf(os.Stderr, "==============================================\n")
		fmt.Fprintf(os.Stderr, "\n")
		return fmt.Errorf("ports %v are already in use", getPortList(unavailablePorts))
	}

	if len(unavailablePorts) > 0 && forceRun {
		fmt.Fprintf(os.Stderr, "\n")
		fmt.Fprintf(os.Stderr, "Force mode: Killing processes using ports...\n")
		for _, p := range unavailablePorts {
			fmt.Fprintf(os.Stderr, "  Killing process on port %d (%s)...\n", p.port, p.name)
			killPort(p.port)
		}
		fmt.Fprintf(os.Stderr, "\n")
	}

	log.Info().Ints("available_ports", availablePorts).Msg("Port check complete")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 初始化 registry 和 pool manager
	reg := registry.New()
	poolMgr := pool.NewManagerWithQueueCapacity(cfg.DefaultMaxConcurrency, cfg.QueueCapacity, cfg.BackendTimeout, log)

	// 初始化 metrics（单例模式，通过 Admin API 的 /metrics 端点暴露）
	metricsInstance := metrics.New()

	// 启动 gossip 服务
	gossipServer, err := gossip.New(cfg, log, reg)
	if err != nil {
		return fmt.Errorf("failed to create gossip server: %w", err)
	}
	defer gossipServer.Shutdown()

	if err := gossipServer.Start(); err != nil {
		return fmt.Errorf("failed to start gossip: %w", err)
	}
	log.Info().Msg("Gossip server started")

	if err := gossipServer.StartAutoDiscovery(); err != nil {
		log.Warn().Err(err).Msg("Failed to start auto-discovery, continuing without it")
	} else {
		log.Info().Msg("Auto-discovery service started")
	}

	// 创建配置中心
	configCenter := config.NewConfigCenter(cfg, gossipServer)
	if err := configCenter.Start(); err != nil {
		log.Error().Err(err).Msg("Failed to start config center")
		return err
	}
	defer configCenter.Shutdown()
	log.Info().Msg("Config center started")

	// 创建 admin 服务
	adminServer := admin.New(cfg, reg, metricsInstance, log, gossipServer, poolMgr)
	log.Info().Msg("Admin server created with registry")

	// 创建业务服务
	businessServer := business.New(cfg, reg, poolMgr, gossipServer, log)
	log.Info().Msg("Business server created with same registry")

	// 创建服务管理器
	serviceMgr := NewServiceManager(adminServer, businessServer, log)

	// 注册服务到配置中心
	configCenter.RegisterConfigListener(adminServer)
	configCenter.RegisterConfigListener(businessServer)

	// 启动所有服务
	serviceMgr.StartAll()

	// 计算 admin 服务地址（ListenAddr - 1）
	var basePort int
	fmt.Sscanf(cfg.ListenAddr, ":%d", &basePort)
	adminAddr := fmt.Sprintf("127.0.0.1:%d", basePort-1)

	// 启动服务监控器（自动重启崩溃的服务）
	go startServiceMonitor(ctx, serviceMgr, adminAddr, log)

	// 定期广播节点状态
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				poolMgr.SnapshotRemoteActives()
				aggregated := reg.AggregateLocalPathInfos()
				gossipStates := make(map[string]*syncsvc.EndpointMetrics, len(aggregated))
				for _, agg := range aggregated {
					gossipStates[agg.NodePath] = &syncsvc.EndpointMetrics{
						NodePath:      agg.NodePath,
						Active:        agg.Active,
						QueueLen:      agg.QueueLen,
						Healthy:       agg.Healthy,
						MaxConcurrent: agg.MaxConcurrent,
						Plugin:        agg.Plugin,
					}
				}
				gossipServer.BroadcastState(gossipStates)
				time.Sleep(cfg.GossipStateInterval)
			}
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	for {
		sig := <-sigCh
		log.Info().Str("signal", sig.String()).Msg("Received signal")

		switch sig {
		case syscall.SIGINT, syscall.SIGTERM:
			log.Info().Msg("Shutting down gracefully...")
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer shutdownCancel()

			// 使用服务管理器停止所有服务
			serviceMgr.StopAll(shutdownCtx)

			configCenter.Shutdown()
			gossipServer.Shutdown()

			log.Info().Msg("Shutdown complete")
			return nil

		case syscall.SIGHUP:
			log.Info().Msg("Reloading configuration...")
			if err := config.Reload(cfg, configCenter); err != nil {
				log.Error().Err(err).Msg("Failed to reload config")
			} else {
				log.Info().Msg("Configuration reloaded")
				logger.SetLevel(cfg.LogLevel)
			}
		}
	}
}

// ServiceManager 服务管理器
type ServiceManager struct {
	adminServer   *admin.AdminServer
	businessServer *business.BusinessServer
	log           zerolog.Logger
	stopped       chan struct{}
	wg            sync.WaitGroup
}

// NewServiceManager 创建服务管理器
func NewServiceManager(adminServer *admin.AdminServer, businessServer *business.BusinessServer, log zerolog.Logger) *ServiceManager {
	return &ServiceManager{
		adminServer:    adminServer,
		businessServer: businessServer,
		log:            log,
		stopped:        make(chan struct{}),
	}
}

// StartAll 启动所有服务
func (sm *ServiceManager) StartAll() error {
	// 启动 admin 服务
	sm.wg.Add(1)
	go func() {
		defer sm.wg.Done()
		if err := sm.adminServer.Start(); err != nil {
			sm.log.Error().Err(err).Msg("Admin server stopped")
		}
	}()

	// 等待 admin 服务启动
	time.Sleep(500 * time.Millisecond)

	// 启动业务服务
	sm.wg.Add(1)
	go func() {
		defer sm.wg.Done()
		if err := sm.businessServer.Start(); err != nil {
			sm.log.Error().Err(err).Msg("Business server stopped")
		}
	}()

	sm.log.Info().Msg("All services started")
	return nil
}

// StopAll 停止所有服务
func (sm *ServiceManager) StopAll(ctx context.Context) {
	sm.log.Info().Msg("Stopping all services...")

	if err := sm.adminServer.Shutdown(ctx); err != nil {
		sm.log.Error().Err(err).Msg("Error shutting down admin server")
	}

	if err := sm.businessServer.Shutdown(ctx); err != nil {
		sm.log.Error().Err(err).Msg("Error shutting down business server")
	}

	sm.wg.Wait()
	sm.log.Info().Msg("All services stopped")
}

// RestartAdmin 重启 admin 服务
func (sm *ServiceManager) RestartAdmin() {
	sm.log.Info().Msg("Restarting admin server...")

	// 关闭旧服务
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sm.adminServer.Shutdown(ctx)

	// 等待一下
	time.Sleep(500 * time.Millisecond)

	// 启动新服务
	sm.wg.Add(1)
	go func() {
		defer sm.wg.Done()
		if err := sm.adminServer.Start(); err != nil {
			sm.log.Error().Err(err).Msg("Failed to restart admin server")
		}
	}()

	sm.log.Info().Msg("Admin server restarted")
}

// RestartBusiness 重启业务服务
func (sm *ServiceManager) RestartBusiness() {
	sm.log.Info().Msg("Restarting business server...")

	// 关闭旧服务
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sm.businessServer.Shutdown(ctx)

	// 等待一下
	time.Sleep(500 * time.Millisecond)

	// 启动新服务
	sm.wg.Add(1)
	go func() {
		defer sm.wg.Done()
		if err := sm.businessServer.Start(); err != nil {
			sm.log.Error().Err(err).Msg("Failed to restart business server")
		}
	}()

	sm.log.Info().Msg("Business server restarted")
}

// startServiceMonitor 启动服务监控器，自动重启崩溃的服务
func startServiceMonitor(
	ctx context.Context,
	serviceMgr *ServiceManager,
	adminAddr string,
	log zerolog.Logger,
) {
	// 服务健康检查间隔
	healthCheckInterval := 10 * time.Second

	ticker := time.NewTicker(healthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("Service monitor stopped")
			return
		case <-ticker.C:
			// 检查 admin 服务健康状态（通过 Admin Server 的健康检查）
			// 如果 Admin Server 不健康，说明整个节点可能有问题，重启所有服务
			if !checkServiceHealth(fmt.Sprintf("http://%s/health", adminAddr), log) {
				log.Warn().Msg("Admin server is not healthy, restarting all services...")
				serviceMgr.RestartAdmin()
				serviceMgr.RestartBusiness()
			}
		}
	}
}

// checkServiceHealth 检查服务健康状态
func checkServiceHealth(url string, log zerolog.Logger) bool {
	client := &http.Client{
		Timeout: 3 * time.Second,
	}
	resp, err := client.Get(url)
	if err != nil {
		log.Debug().Str("url", url).Err(err).Msg("Health check failed")
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Debug().Str("url", url).Int("status", resp.StatusCode).Msg("Health check returned non-OK status")
		return false
	}

	return true
}

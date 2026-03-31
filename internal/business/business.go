package business

import (
	"net/http"

	"github.com/rs/zerolog"

	"novaairouter/internal/business/balancer"
	"novaairouter/internal/business/forwarder"
	"novaairouter/internal/business/router"
	"novaairouter/internal/config"
	"novaairouter/internal/gossip"
	"novaairouter/internal/pool"
	"novaairouter/internal/registry"
)

type BusinessServer struct {
	config    *config.Config
	registry  *registry.Registry
	poolMgr   *pool.PoolManager
	gossip    *gossip.GossipServer
	log       zerolog.Logger
	server    *http.Server
	router    *router.Router
	balancer  *balancer.Balancer
	forwarder *forwarder.Forwarder
	accessLog interface{ Log(interface{}); Close() }
}

func New(cfg *config.Config, reg *registry.Registry, poolMgr *pool.PoolManager, gossipServer *gossip.GossipServer, log zerolog.Logger) *BusinessServer {
	router := router.New(cfg, reg, log)
	balancer := balancer.New()
	forwarder := forwarder.New(cfg, reg, poolMgr, log)

	return &BusinessServer{
		config:    cfg,
		registry:  reg,
		poolMgr:   poolMgr,
		gossip:    gossipServer,
		log:       log,
		router:    router,
		balancer:  balancer,
		forwarder: forwarder,
	}
}

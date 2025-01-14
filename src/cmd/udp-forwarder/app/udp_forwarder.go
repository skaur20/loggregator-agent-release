package app

import (
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof"

	metrics "code.cloudfoundry.org/go-metric-registry"
	v1 "code.cloudfoundry.org/loggregator-agent-release/src/pkg/egress/v1"

	"code.cloudfoundry.org/go-loggregator/v9"
	"code.cloudfoundry.org/go-loggregator/v9/conversion"
	ingress "code.cloudfoundry.org/loggregator-agent-release/src/pkg/ingress/v1"
	"github.com/cloudfoundry/sonde-go/events"
)

type Metrics interface {
	NewGauge(name, helpText string, opts ...metrics.MetricOption) metrics.Gauge
	NewCounter(name, helpText string, opts ...metrics.MetricOption) metrics.Counter
	RegisterDebugMetrics()
}

type UDPForwarder struct {
	grpc         GRPC
	udpPort      int
	pprofServer  *http.Server
	pprofPort    uint16
	debugMetrics bool
	log          *log.Logger
	metrics      Metrics
	deployment   string
	job          string
	index        string
	ip           string
}

func NewUDPForwarder(cfg Config, l *log.Logger, m Metrics) *UDPForwarder {
	return &UDPForwarder{
		grpc:         cfg.LoggregatorAgentGRPC,
		udpPort:      cfg.UDPPort,
		pprofPort:    cfg.MetricsServer.PprofPort,
		debugMetrics: cfg.MetricsServer.DebugMetrics,
		log:          l,
		metrics:      m,
		deployment:   cfg.Deployment,
		job:          cfg.Job,
		index:        cfg.Index,
		ip:           cfg.IP,
	}
}

func (u *UDPForwarder) Run() {
	if u.debugMetrics {
		u.metrics.RegisterDebugMetrics()
		u.pprofServer = &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", u.pprofPort), Handler: http.DefaultServeMux}
		go func() { log.Println("PPROF SERVER STOPPED " + u.pprofServer.ListenAndServe().Error()) }()
	}
	tlsConfig, err := loggregator.NewIngressTLSConfig(
		u.grpc.CAFile,
		u.grpc.CertFile,
		u.grpc.KeyFile,
	)
	if err != nil {
		u.log.Fatalf("Failed to create loggregator agent credentials: %s", err)
	}

	v2Ingress, err := loggregator.NewIngressClient(
		tlsConfig,
		loggregator.WithLogger(u.log),
		loggregator.WithAddr(u.grpc.Addr),
	)
	if err != nil {
		u.log.Fatalf("Failed to create loggregator agent client: %s", err)
	}

	w := v1.NewTagger(
		u.deployment,
		u.job,
		u.index,
		u.ip,
		v2Writer{v2Ingress},
	)

	dropsondeUnmarshaller := ingress.NewUnMarshaller(w)
	networkReader, err := ingress.NewNetworkReader(
		fmt.Sprintf("127.0.0.1:%d", u.udpPort),
		dropsondeUnmarshaller,
		u.metrics,
	)
	if err != nil {
		u.log.Fatalf("Failed to listen on 127.0.0.1:%d: %s", u.udpPort, err)
	}

	go networkReader.StartReading()
	networkReader.StartWriting()
}
func (u *UDPForwarder) Stop() {
	if u.pprofServer != nil {
		u.pprofServer.Close()
	}
}

type v2Writer struct {
	ingressClient *loggregator.IngressClient
}

func (w v2Writer) Write(e *events.Envelope) {
	v2e := conversion.ToV2(e, true)
	w.ingressClient.Emit(v2e)
}

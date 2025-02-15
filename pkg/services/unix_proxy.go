//go:build !no_proxies && !no_services
// +build !no_proxies,!no_services

package services

import (
	"fmt"
	"net"
	"os"
	"runtime"

	"github.com/ansible/receptor/pkg/logger"
	"github.com/ansible/receptor/pkg/netceptor"
	"github.com/ansible/receptor/pkg/tls"
	"github.com/ansible/receptor/pkg/utils"
	"github.com/ghjm/cmdline"
)

// UnixProxyServiceInbound listens on a Unix socket and forwards connections over the Receptor network.
func UnixProxyServiceInbound(s *netceptor.Netceptor, filename string, permissions os.FileMode,
	node string, rservice string, tlscfg *tls.Config) error {
	uli, lock, err := utils.UnixSocketListen(filename, permissions)
	if err != nil {
		return fmt.Errorf("error opening Unix socket: %s", err)
	}
	go func() {
		defer lock.Unlock()
		for {
			uc, err := uli.Accept()
			if err != nil {
				logger.Error("Error accepting Unix socket connection: %s", err)

				return
			}
			go func() {
				qc, err := s.Dial(node, rservice, tlscfg)
				if err != nil {
					logger.Error("Error connecting on Receptor network: %s", err)

					return
				}
				utils.BridgeConns(uc, "unix socket service", qc, "receptor connection")
			}()
		}
	}()

	return nil
}

// UnixProxyServiceOutbound listens on the Receptor network and forwards the connection via a Unix socket.
func UnixProxyServiceOutbound(s *netceptor.Netceptor, service string, tlscfg *tls.Config, filename string) error {
	qli, err := s.ListenAndAdvertise(service, tlscfg, map[string]string{
		"type":     "Unix Proxy",
		"filename": filename,
	})
	if err != nil {
		return fmt.Errorf("error listening on Receptor network: %s", err)
	}
	go func() {
		for {
			qc, err := qli.Accept()
			if err != nil {
				logger.Error("Error accepting connection on Receptor network: %s\n", err)

				return
			}
			uc, err := net.Dial("unix", filename)
			if err != nil {
				logger.Error("Error connecting via Unix socket: %s\n", err)

				continue
			}
			go utils.BridgeConns(qc, "receptor service", uc, "unix socket connection")
		}
	}()

	return nil
}

// unixProxyInboundCfg is the cmdline configuration object for a Unix socket inbound proxy.
type unixProxyInboundCfg struct {
	Filename      string `required:"true" description:"Socket filename, which will be overwritten"`
	Permissions   int    `description:"Socket file permissions" default:"0600"`
	RemoteNode    string `required:"true" description:"Receptor node to connect to"`
	RemoteService string `required:"true" description:"Receptor service name to connect to"`
	TLS           string `description:"Name of TLS client config for the Receptor connection"`
}

// Run runs the action.
func (cfg unixProxyInboundCfg) Run() error {
	logger.Debug("Running Unix socket inbound proxy service %v\n", cfg)
	tlscfg, err := netceptor.MainInstance.GetClientTLSConfig(cfg.TLS, cfg.RemoteNode, "receptor")
	if err != nil {
		return err
	}

	return UnixProxyServiceInbound(netceptor.MainInstance, cfg.Filename, os.FileMode(cfg.Permissions),
		cfg.RemoteNode, cfg.RemoteService, tlscfg)
}

// unixProxyOutboundCfg is the cmdline configuration object for a Unix socket outbound proxy.
type unixProxyOutboundCfg struct {
	Service  string `required:"true" description:"Receptor service name to bind to"`
	Filename string `required:"true" description:"Socket filename, which must already exist"`
	TLS      string `description:"Name of TLS server config for the Receptor connection"`
}

// Run runs the action.
func (cfg unixProxyOutboundCfg) Run() error {
	logger.Debug("Running Unix socket inbound proxy service %s\n", cfg)
	tlscfg, err := netceptor.MainInstance.GetServerTLSConfig(cfg.TLS)
	if err != nil {
		return err
	}

	return UnixProxyServiceOutbound(netceptor.MainInstance, cfg.Service, tlscfg, cfg.Filename)
}

func init() {
	if runtime.GOOS != "windows" {
		cmdline.RegisterConfigTypeForApp("receptor-proxies",
			"unix-socket-server", "Listen on a Unix socket and forward via Receptor", unixProxyInboundCfg{}, cmdline.Section(servicesSection))
		cmdline.RegisterConfigTypeForApp("receptor-proxies",
			"unix-socket-client", "Listen via Receptor and forward to a Unix socket", unixProxyOutboundCfg{}, cmdline.Section(servicesSection))
	}
}

// UnixInProxy exposes an exported unix socket.
type UnixInProxy struct {
	// Socket filename, which will be overwritten.
	File string `mapstructure:"file"`
	// Socket file permissions.
	Permissions *int `mapstructure:"permissions"`
	// Receptor node to connect to.
	RemoteNode string `mapstructure:"remote-node"`
	// Receptor service name to connect to.
	RemoteService string `mapstructure:"remote-service"`
	// TLS config to use for the transport within receptor.
	// Leave empty for no TLS.
	TLS tls.ClientConf `mapstructure:"tls"`
}

func (p *UnixInProxy) setup(nc *netceptor.Netceptor) error {
	perms := 0o600
	if p.Permissions != nil {
		perms = *p.Permissions
	}

	t, err := p.TLS.TLSConfig()
	if err != nil {
		return fmt.Errorf("could not create tls config for unix inbound proxy %s: %w", p.File, err)
	}

	return UnixProxyServiceInbound(
		nc,
		p.File,
		os.FileMode(perms),
		p.RemoteNode,
		p.RemoteService,
		t,
	)
}

// UnixOutProxy exports a local unix socket.
type UnixOutProxy struct {
	// Receptor service name to bind to.
	Service string `mapstructure:"service"`
	// Socket filename, which must already exist.
	File string `mapstructure:"file"`
	// TLS config to use for the transport within receptor.
	// Leave empty for no TLS.
	TLS tls.ServerConf `mapstructure:"tls"`
}

func (p *UnixOutProxy) setup(nc *netceptor.Netceptor) error {
	t, err := p.TLS.TLSConfig()
	if err != nil {
		return fmt.Errorf("could not create tls config for unix outbound proxy %s: %w", p.File, err)
	}

	return UnixProxyServiceOutbound(nc, p.Service, t, p.File)
}

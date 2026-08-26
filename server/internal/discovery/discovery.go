package discovery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/grandcat/zeroconf"
)

const (
	ServiceType       = "_rivune._tcp"
	serviceDomain     = "local."
	defaultInstance   = "Rivune"
	protocolVersion   = 22
	maximumTXTLength  = 255
	maximumNameLength = 63
)

type Config struct {
	InstanceName string
	Origin       string
	Port         int
}

type registerFunc func(instance, service, domain string, port int, text []string, ifaces []net.Interface) (func(), error)

func Load(lookupEnv func(string) (string, bool)) (Config, error) {
	if lookupEnv == nil {
		return Config{}, errors.New("environment lookup is required")
	}
	rawOrigin, _ := lookupEnv("RIVUNE_DISCOVERY_URL")
	origin, port, err := parseOrigin(rawOrigin)
	if err != nil {
		return Config{}, err
	}
	instanceName, _ := lookupEnv("RIVUNE_DISCOVERY_NAME")
	instanceName = strings.TrimSpace(instanceName)
	if instanceName == "" {
		instanceName = defaultInstance
	}
	if !utf8.ValidString(instanceName) || len(instanceName) > maximumNameLength || strings.ContainsAny(instanceName, "\x00\r\n") {
		return Config{}, fmt.Errorf("RIVUNE_DISCOVERY_NAME must be valid UTF-8 without control characters and at most %d bytes", maximumNameLength)
	}
	return Config{InstanceName: instanceName, Origin: origin, Port: port}, nil
}

func parseOrigin(raw string) (string, int, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", 0, errors.New("RIVUNE_DISCOVERY_URL is required")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" || parsed.RawPath != "" || parsed.ForceQuery || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return "", 0, errors.New("RIVUNE_DISCOVERY_URL must be an absolute HTTP(S) origin without credentials, path, query, or fragment")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", 0, errors.New("RIVUNE_DISCOVERY_URL must use http or https")
	}
	host := parsed.Hostname()
	if host == "" || strings.EqualFold(host, "localhost") {
		return "", 0, errors.New("RIVUNE_DISCOVERY_URL must identify a LAN-reachable or HTTPS server, not loopback")
	}
	if address, parseErr := netip.ParseAddr(host); parseErr == nil {
		address = address.Unmap()
		if address.IsLoopback() || address.IsUnspecified() || address.IsMulticast() || address.IsLinkLocalUnicast() {
			return "", 0, errors.New("RIVUNE_DISCOVERY_URL must identify a LAN-reachable or HTTPS server, not loopback, unspecified, multicast, or link-local")
		}
		if parsed.Scheme == "http" && !address.IsPrivate() {
			return "", 0, errors.New("RIVUNE_DISCOVERY_URL must use HTTPS unless its host is a private-network IP address")
		}
	} else if parsed.Scheme == "http" {
		return "", 0, errors.New("RIVUNE_DISCOVERY_URL must use HTTPS unless its host is a private-network IP address")
	}
	port := 443
	if parsed.Scheme == "http" {
		port = 80
	}
	if rawPort := parsed.Port(); rawPort != "" {
		port, err = strconv.Atoi(rawPort)
		if err != nil || port < 1 || port > 65535 {
			return "", 0, errors.New("RIVUNE_DISCOVERY_URL contains an invalid port")
		}
	}
	parsed.Path = ""
	origin := parsed.String()
	if len("url="+origin) > maximumTXTLength {
		return "", 0, fmt.Errorf("RIVUNE_DISCOVERY_URL is too long for DNS-SD TXT records (maximum %d bytes including the url key)", maximumTXTLength)
	}
	return origin, port, nil
}

func Run(ctx context.Context, logger *slog.Logger, cfg Config, version string) error {
	return run(ctx, logger, cfg, version, func(instance, service, domain string, port int, text []string, ifaces []net.Interface) (func(), error) {
		server, err := zeroconf.Register(instance, service, domain, port, text, ifaces)
		if err != nil {
			return nil, err
		}
		return server.Shutdown, nil
	})
}

func run(ctx context.Context, logger *slog.Logger, cfg Config, version string, register registerFunc) error {
	if ctx == nil {
		return errors.New("discovery context is required")
	}
	if logger == nil {
		return errors.New("discovery logger is required")
	}
	if register == nil {
		return errors.New("discovery registrar is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	version = strings.TrimSpace(version)
	if version == "" || len("version="+version) > maximumTXTLength || strings.ContainsAny(version, "=\x00\r\n") {
		return errors.New("server version is not valid for DNS-SD")
	}
	text := []string{
		"url=" + cfg.Origin,
		"protocol=" + strconv.Itoa(protocolVersion),
		"version=" + version,
	}
	shutdown, err := register(cfg.InstanceName, ServiceType, serviceDomain, cfg.Port, text, nil)
	if err != nil {
		return fmt.Errorf("register Rivune DNS-SD service: %w", err)
	}
	if shutdown == nil {
		return errors.New("register Rivune DNS-SD service: registrar returned no shutdown function")
	}
	logger.Info("Rivune LAN discovery active", "service", ServiceType, "instance", cfg.InstanceName, "origin", cfg.Origin)
	<-ctx.Done()
	shutdown()
	return nil
}

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"

	"github.com/ASSever/debian-router-b5-2840/backend/internal/proxy"
)

type config struct {
	Group    string `json:"group"`
	URL      string `json:"probe_url"`
	CA       string `json:"ca_file"`
	Status   int    `json:"expected_status"`
	Interval int    `json:"interval_seconds"`
	tls      *tls.Config
}

var groupName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,23}$`)
var alternateUnit = regexp.MustCompile(`^debian-router-sing-box-[a-z0-9-]{1,25}\.service$`)

func readConfig(path string) (config, error) {
	cfg := config{URL: proxy.CheckURL, Status: 204, Interval: 15}
	f, err := os.Open(path)
	if err != nil {
		return cfg, err
	}
	defer f.Close()
	const maxConfig = 16 * 1024
	data, err := io.ReadAll(io.LimitReader(f, maxConfig+1))
	if err != nil {
		return cfg, err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if len(data) > maxConfig || d.Decode(&cfg) != nil || !errors.Is(d.Decode(new(any)), io.EOF) {
		return cfg, errors.New("invalid proxy recovery configuration")
	}
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return cfg, errors.New("proxy recovery probe needs an HTTPS URL without credentials, query or fragment")
	}
	if !groupName.MatchString(cfg.Group) || cfg.Status < 200 || cfg.Status > 299 ||
		cfg.Interval < 1 || cfg.Interval > 300 {
		return cfg, errors.New("invalid proxy recovery group, status or interval")
	}
	if cfg.CA != "" {
		data, err := os.ReadFile(cfg.CA)
		roots := x509.NewCertPool()
		if err != nil || !roots.AppendCertsFromPEM(data) {
			return cfg, errors.New("invalid proxy recovery CA file")
		}
		cfg.tls = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	}
	return cfg, nil
}

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	path := flag.String("config", "/etc/debian-router-b5-2840/recovery.json", "recovery configuration")
	state := flag.String("state-dir", proxy.ProfileStatePath, "active profile state directory")
	unit := flag.String("proxy-unit", proxy.ProxyService, "sing-box unit for this profile state")
	flag.Parse()
	if flag.NArg() > 1 || (flag.NArg() == 1 && flag.Arg(0) != "check-config") {
		return errors.New("usage: router-recovery [options] [check-config]")
	}
	if (*state == proxy.ProfileStatePath && *unit != proxy.ProxyService) ||
		(*state != proxy.ProfileStatePath && !alternateUnit.MatchString(*unit)) {
		return errors.New("alternate proxy state needs its own sing-box instance")
	}
	cfg, err := readConfig(*path)
	if err != nil {
		return err
	}
	if flag.NArg() == 1 {
		fmt.Println("Proxy recovery configuration valid.")
		return nil
	}
	recovery := proxy.Recovery{
		Manager: proxy.NewProfileManagerForUnit(*state, *unit),
		Group:   cfg.Group, URL: cfg.URL, Trust: cfg.tls,
		StatusCode: cfg.Status, Logger: log.Default(),
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	for ctx.Err() == nil {
		if err := recovery.Tick(ctx); err != nil && ctx.Err() == nil {
			log.Print(err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Duration(cfg.Interval) * time.Second):
		}
	}
	return nil
}

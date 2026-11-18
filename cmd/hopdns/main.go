// HopDNS is a terminal application with an embedded browser panel and proxy.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/mkay1375/hopdns/internal/control"
	"github.com/mkay1375/hopdns/internal/engine"
)

var version = "dev"
var commit = "unknown"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "engine" {
		engine.Run(os.Args[2:])
		return
	}
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "HopDNS:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = "."
	}
	defaultData := filepath.Join(configDir, "hopdns")
	if value := os.Getenv("HOPDNS_DATA"); value != "" {
		defaultData = value
	}
	flags := flag.NewFlagSet("hopdns", flag.ContinueOnError)
	panel := flags.String("panel", "127.0.0.1:48741", "loopback address for the browser panel (port 0 chooses an available port)")
	trafficBind := flags.String("traffic-bind", "0.0.0.0", "IPv4 interface for DNS and proxy listeners; use 127.0.0.1 for local-only access")
	dnsPort := flags.Int("dns-port", 53, "DNS UDP and TCP port")
	httpPort := flags.Int("http-port", 80, "HTTP relay port")
	httpsPort := flags.Int("https-port", 443, "HTTPS relay port")
	dataDir := flags.String("data-dir", defaultData, "directory for saved routing configuration")
	publicIP := flags.String("public-ip", os.Getenv("HOPDNS_PUBLIC_IPV4"), "initial IPv4 address advertised to clients; saved settings take precedence")
	noBrowser := flags.Bool("no-browser", false, "print the panel URL without opening a browser")
	showVersion := flags.Bool("version", false, "print the application version and source commit")
	if err = flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected arguments; use --help for usage")
	}
	if *showVersion {
		fmt.Printf("HopDNS %s (%s), %s/%s\n", version, commit, runtime.GOOS, runtime.GOARCH)
		return nil
	}
	panelAddress, err := panelBindAddress(*panel)
	if err != nil {
		return err
	}
	absoluteData, err := filepath.Abs(*dataDir)
	if err != nil {
		return err
	}
	options := control.Options{DataDir: absoluteData, PublicIPv4: *publicIP, TrafficBind: *trafficBind, DNSPort: *dnsPort, HTTPPort: *httpPort, HTTPSPort: *httpsPort, Log: os.Stderr}
	if err = options.Validate(); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", panelAddress)
	if err != nil {
		return fmt.Errorf("open browser panel: %w; choose a different --panel port", err)
	}
	defer listener.Close()
	if *trafficBind == "0.0.0.0" && *publicIP == "" {
		options.DetectedIPv4 = detectLANIPv4()
	}
	controller, err := control.New(options)
	if err != nil {
		return err
	}
	defer controller.Close()
	server := control.HTTPServer(listener.Addr().String(), controller.Handler())
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	url := "http://" + listener.Addr().String()
	fmt.Printf("HopDNS %s\nPanel: %s\nSettings: %s\nDNS: %s:%d (UDP/TCP)\nHTTP: %s:%d\nHTTPS: %s:%d\n", version, url, absoluteData, *trafficBind, *dnsPort, *trafficBind, *httpPort, *trafficBind, *httpsPort)
	if address := controller.Config().PublicIPv4; address != "" {
		fmt.Printf("DNS address for your console: %s\n", address)
	}
	fmt.Println("Keep this terminal open. Press Ctrl+C to stop HopDNS.")
	if *trafficBind != "127.0.0.1" {
		fmt.Println("LAN/server listeners enabled. Restrict access to trusted clients with your host or provider firewall.")
	}
	fmt.Println("If the relay reports a port or permission error, free the port or run with the privileges required by your operating system.")
	if !*noBrowser {
		if err = openBrowser(url); err != nil {
			fmt.Fprintln(os.Stderr, "Open the panel URL in your browser:", err)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case <-ctx.Done():
	case err = <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return server.Shutdown(shutdown)
}

func panelBindAddress(address string) (string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", errors.New("panel must use a loopback address and port, for example 127.0.0.1:48741")
	}
	if !control.IsPanelHost(host) {
		return "", errors.New("panel must bind to localhost, 127.0.0.1 or ::1")
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 0 || number > 65535 {
		return "", errors.New("invalid panel port")
	}
	if host == "localhost" {
		// Avoid a resolver-dependent bind and keep the printed URL on loopback.
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, strconv.Itoa(number)), nil
}

func openBrowser(url string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", url)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		command = exec.Command("xdg-open", url)
	}
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	return nil
}

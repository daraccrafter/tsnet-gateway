package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"tailscale.com/tsnet"
)

var srv *tsnet.Server
var routeConfig = make(map[string]string)

func main() {
	defaultBaseDir, err := os.Getwd()
	if err != nil {
		log.Fatalf("Failed to get current directory: %v", err)
	}

	authKey := flag.String("authkey", "", "Tailscale auth key")
	baseDir := flag.String("base", defaultBaseDir, "Base directory for Tailscale data")
	proxyPort := flag.Int("proxy-port", 8080, "Port to listen on")
	routesArg := flag.String("routes", "", "Comma-separated route mappings (e.g. '/api/=http://localhost:9696,/app/=http://localhost:8081')")
	routesFile := flag.String("routes-file", "", "Path to a JSON file defining route mappings")
	proxyType := flag.String("type", "gateway", "Specify mode: rproxy (reverse proxy), proxy (outgoing proxy), or gateway (both)")
	rproxyPort := flag.Int("rproxy-port", 8443, "Port to listen on for reverse proxy")
	hostname := flag.String("hostname", "tsnet-gateway", "Hostname to use for the Tailscale node")
	flag.Parse()

	logDir := filepath.Join(*baseDir, "tsnet-gateway", "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Fatalf("Failed to create log directory: %v", err)
	}

	// Set up logging to both console and file
	logFilePath := filepath.Join(logDir, "tsnet-gateway.log")
	logFile, err := os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("Failed to open log file: %v", err)
	}
	defer logFile.Close()

	log.SetOutput(io.MultiWriter(os.Stdout, logFile))

	if *authKey == "" {
		log.Fatal("Error: --authkey is required")
	}

	if *proxyType == "rproxy" || *proxyType == "gateway" {
		if err := loadRoutes(*routesArg, *routesFile); err != nil {
			log.Fatalf("Failed to load routes: %v", err)
		}
	}
	srv = &tsnet.Server{
		Hostname: *hostname,
		AuthKey:  *authKey,
		Logf:     log.Printf,
		Dir:      fmt.Sprintf("%s/Tailscale", *baseDir),
	}

	if err := srv.Start(); err != nil {
		log.Fatalf("Failed to start Tailscale: %v", err)
	}

	if *proxyType == "proxy" || *proxyType == "gateway" {
		go startProxy(*proxyPort)
	}
	if *proxyType == "rproxy" || *proxyType == "gateway" {
		go startTLSListener(*rproxyPort)
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)
	<-sigChan
	log.Println("Shutting down...")
}

func loadRoutes(routesArg, routesFile string) error {
	if routesArg != "" {
		pairs := strings.Split(routesArg, ",")
		for _, pair := range pairs {
			parts := strings.SplitN(pair, "=", 2)
			if len(parts) != 2 {
				return fmt.Errorf("invalid route format: %s", pair)
			}
			routeConfig[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
		log.Printf("Loaded routes from command-line argument: %+v", routeConfig)
		return nil
	}

	if routesFile != "" {
		data, err := ioutil.ReadFile(routesFile)
		if err != nil {
			return fmt.Errorf("failed to read routes file: %w", err)
		}

		err = json.Unmarshal(data, &routeConfig)
		if err != nil {
			return fmt.Errorf("failed to parse routes file: %w", err)
		}

		log.Printf("Loaded routes from file: %+v", routeConfig)
		return nil
	}

	return nil
}

func startProxy(proxyPort int) {
	proxyHandler := http.HandlerFunc(handleProxyRequest)
	msg := fmt.Sprintf("Proxy server started on http://localhost:%d", proxyPort)
	log.Println(msg)
	err := http.ListenAndServe(fmt.Sprintf("localhost:%d", proxyPort), proxyHandler)
	if err != nil {
		log.Fatalf("Proxy server failed: %v", err)
	}
}

func startTLSListener(rproxyPort int) {

	ln, err := srv.ListenTLS("tcp", fmt.Sprintf(":%d", rproxyPort))
	if err != nil {
		log.Fatalf("Failed to start TLS listener: %v", err)
	}
	defer ln.Close()

	log.Println("TLS server started on port 443 (inside Tailnet)")
	http.Serve(ln, http.HandlerFunc(routeRequest))
}

func routeRequest(w http.ResponseWriter, r *http.Request) {
	log.Printf("Received request: %s %s", r.Method, r.URL.Path)

	for prefix, backendURL := range routeConfig {
		if strings.HasPrefix(r.URL.Path, prefix) {
			log.Printf("Forwarding request %s to backend %s", r.URL.Path, backendURL)
			forwardRequest(backendURL, prefix, w, r)
			return
		}
	}

	http.Error(w, "No matching route", http.StatusNotFound)
}

func forwardRequest(backendURL, prefix string, w http.ResponseWriter, r *http.Request) {
	target, err := url.Parse(backendURL)
	if err != nil {
		http.Error(w, "Invalid backend URL", http.StatusInternalServerError)
		return
	}

	r.URL.Path = strings.TrimPrefix(r.URL.Path, prefix)
	if !strings.HasPrefix(r.URL.Path, "/") {
		r.URL.Path = "/" + r.URL.Path
	}

	log.Printf("Stripped path: Forwarding to %s%s", backendURL, r.URL.Path)

	proxy := httputil.NewSingleHostReverseProxy(target)
	r.URL.Host = target.Host
	r.URL.Scheme = target.Scheme
	r.Host = target.Host

	proxy.ServeHTTP(w, r)
}

func handleProxyRequest(w http.ResponseWriter, r *http.Request) {
	log.Printf("Incoming request: %s %s", r.Method, r.URL.String())

	if r.Method == http.MethodConnect {
		handleHTTPSProxy(w, r)
		return
	}

	handleHTTPProxy(w, r)
}

func handleHTTPSProxy(w http.ResponseWriter, r *http.Request) {
	destConn, err := srv.Dial(r.Context(), "tcp", r.Host)
	if err != nil {
		log.Printf("Failed to connect to target: %v", err)
		http.Error(w, "Failed to connect to target", http.StatusBadGateway)
		return
	}
	defer destConn.Close()

	w.WriteHeader(http.StatusOK)

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		log.Println("Hijacking not supported")
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		log.Printf("Failed to hijack connection: %v", err)
		http.Error(w, "Failed to hijack connection", http.StatusInternalServerError)
		return
	}
	defer clientConn.Close()

	go io.Copy(destConn, clientConn)
	io.Copy(clientConn, destConn)
}

func handleHTTPProxy(w http.ResponseWriter, r *http.Request) {
	destConn, err := srv.Dial(r.Context(), "tcp", r.Host)
	if err != nil {
		log.Printf("Failed to connect to target: %v", err)
		http.Error(w, "Failed to connect to target", http.StatusBadGateway)
		return
	}
	defer destConn.Close()

	if err := r.Write(destConn); err != nil {
		log.Printf("Failed to forward request: %v", err)
		http.Error(w, "Failed to forward request", http.StatusInternalServerError)
		return
	}

	io.Copy(w, destConn)
}

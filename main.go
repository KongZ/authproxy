package main

import (
	"crypto/tls"
	"errors"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"time"

	"golang.org/x/exp/slices"
	"gopkg.in/yaml.v3"
)

var Audit = false
var Debug = false
var ErrUnauthorizedHeaders = errors.New("unauthorized headers")
var AcceptedHeaders map[string][]string
var DeniedPaths map[string][]string

type AcceptedHeadersStruct []struct {
	Name   string   `yaml:"name"`
	Values []string `yaml:"values"`
}

type DeniedPathsStruct []struct {
	Name        string   `yaml:"name"`
	HeaderValue []string `yaml:"headerValue"`
	Paths       []string `yaml:"paths"`
}

type transport struct {
	http.RoundTripper
	Host           string
	HealthcheckUrl string
}

// NewProxy takes target host and creates a reverse proxy
func NewProxy(targetHost string) (*httputil.ReverseProxy, error) {
	url, err := url.Parse(targetHost)
	if err != nil {
		return nil, err
	}
	proxy := httputil.NewSingleHostReverseProxy(url)
	targetHealth := os.Getenv("TARGET_HEALTHCHECK")
	if targetHealth == "" {
		targetHealth = "/"
	}
	skipCert, _ := strconv.ParseBool(os.Getenv("IGNORE_CERTIFICATE"))
	proxy.Transport = &transport{
		RoundTripper: &http.Transport{
			// #nosec G402 allow
			TLSClientConfig: &tls.Config{InsecureSkipVerify: skipCert},
		},
		Host:           url.Host,
		HealthcheckUrl: targetHealth,
	}
	proxy.ErrorHandler = errorHandler()
	return proxy, nil
}

func (t *transport) RoundTrip(req *http.Request) (resp *http.Response, err error) {
	req.Host = t.Host
	reqPath := req.URL.Path
	// Debug
	if Debug {
		log.Println("[Request Headers]")
		log.Printf("Path: %s", reqPath)
		for name, values := range req.Header {
			// Loop over all values for the name.
			for _, value := range values {
				log.Printf("%s: %v", name, value)
			}
		}
	}
	// skip validating for healthcheck
	if t.HealthcheckUrl == reqPath {
		return t.RoundTripper.RoundTrip(req)
	}
	var reqValue string
	for key, values := range AcceptedHeaders {
		reqValue = req.Header.Get(key)
		if Debug || (Audit && reqValue != "") {
			log.Printf("Requested Value %s=%s", key, reqValue)
		}
		if !slices.Contains(values, reqValue) {
			resp := &http.Response{
				StatusCode: http.StatusForbidden,
			}
			return resp, ErrUnauthorizedHeaders
		}
	}
	if deninedPaths, found := DeniedPaths[reqValue]; found {
		if Debug {
			log.Printf("Requested path [%s]", reqPath)
		}
		if slices.Contains(deninedPaths, reqPath) {
			resp := &http.Response{
				StatusCode: http.StatusForbidden,
			}
			return resp, ErrUnauthorizedHeaders
		}
	}
	// Debug
	if Debug {
		resp, er := t.RoundTripper.RoundTrip(req)
		log.Println("[Response Headers]")
		if er == nil {
			log.Printf("[Status=%d]", resp.StatusCode)
			for name, values := range resp.Header {
				// Loop over all values for the name.
				for _, value := range values {
					log.Printf("%s: %v", name, value)
				}
			}
		}
		return resp, er
	}
	return t.RoundTripper.RoundTrip(req)
}

func errorHandler() func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, req *http.Request, err error) {
		if errors.Is(err, ErrUnauthorizedHeaders) {
			w.WriteHeader(http.StatusForbidden)
		}
	}
}

// ProxyRequestHandler handles the http request using proxy
func ProxyRequestHandler(proxy *httputil.ReverseProxy) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		proxy.ServeHTTP(w, r)
	}
}

func configure() {
	AcceptedHeaders = make(map[string][]string)
	acceptedHeadersJson := os.Getenv("ACCEPTED_HEADERS")
	var acceptedHeaders AcceptedHeadersStruct
	if err := yaml.Unmarshal([]byte(acceptedHeadersJson), &acceptedHeaders); err != nil {
		log.Printf("Invalid ACCEPTED_HEADERS [%v]", err)
	}
	for _, v := range acceptedHeaders {
		AcceptedHeaders[v.Name] = v.Values
	}
	DeniedPaths = make(map[string][]string)
	deniedPathsJson := os.Getenv("DENIED_PATHS")
	var deniedPaths DeniedPathsStruct
	if err := yaml.Unmarshal([]byte(deniedPathsJson), &deniedPaths); err != nil {
		log.Printf("Invalid DENIED_PATHS [%v]", err)
	}
	for _, v := range deniedPaths {
		for _, i := range v.HeaderValue {
			DeniedPaths[i] = append(DeniedPaths[i], v.Paths...)
		}
	}
}

func main() {
	Debug, _ = strconv.ParseBool(os.Getenv("DEBUG"))
	Audit, _ = strconv.ParseBool(os.Getenv("AUDIT"))
	targetUrl := os.Getenv("TARGET_URL")
	proxyPort := os.Getenv("PROXY_PORT")
	if proxyPort == "" {
		proxyPort = "8080"
	}
	// initialize a reverse proxy and pass the actual backend server url here
	log.Printf("target url [%s]", targetUrl)
	configure()
	proxy, err := NewProxy(targetUrl)
	if err != nil {
		panic(err)
	}
	http.HandleFunc("/", ProxyRequestHandler(proxy))
	log.Printf("running proxy on port [%s]", proxyPort)
	server := &http.Server{
		Addr:        ":" + proxyPort,
		ReadTimeout: 60 * time.Second,
		Handler:     nil,
	}
	log.Fatal(server.ListenAndServe())
}

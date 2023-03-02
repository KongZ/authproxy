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
	"strings"
	"time"

	"golang.org/x/exp/slices"
)

var ErrUnauthorizedHeaders = errors.New("unauthorized headers")

// NewProxy takes target host and creates a reverse proxy
func NewProxy(targetHost string, acceptedHeaders map[string][]string) (*httputil.ReverseProxy, error) {
	url, err := url.Parse(targetHost)
	if err != nil {
		return nil, err
	}
	targetUrl := os.Getenv("TARGET_URL")
	u, err := url.Parse(targetUrl)
	if err != nil {
		return nil, errors.New("invalid target url")
	}
	proxy := httputil.NewSingleHostReverseProxy(url)
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		req.Host = u.Host
		originalDirector(req)
		err = modifyRequest(req, acceptedHeaders)
	}
	skipCert, _ := strconv.ParseBool(os.Getenv("IGNORE_CERTIFICATE"))
	proxy.Transport = &http.Transport{
		// #nosec G402 allow
		TLSClientConfig: &tls.Config{InsecureSkipVerify: skipCert},
	}
	proxy.ModifyResponse = modifyResponse(err)
	proxy.ErrorHandler = errorHandler()
	return proxy, nil
}

func modifyRequest(req *http.Request, acceptedHeaders map[string][]string) error {
	for key, values := range acceptedHeaders {
		reqValue := req.Header.Get(key)
		if !slices.Contains(values, reqValue) {
			return ErrUnauthorizedHeaders
		}
	}
	// // Debug
	// log.Println("[Request]")
	// for name, values := range req.Header {
	// 	// Loop over all values for the name.
	// 	for _, value := range values {
	// 		log.Printf("%s: %v", name, value)
	// 	}
	// }
	return nil
}

func modifyResponse(err error) func(*http.Response) error {
	return func(resp *http.Response) error {
		if err != nil {
			if errors.Is(err, ErrUnauthorizedHeaders) {
				resp.StatusCode = http.StatusForbidden
			}
		}
		// // Debug
		// log.Println("[Response]")
		// log.Printf("[Status=%d]", resp.StatusCode)
		// for name, values := range resp.Header {
		// 	// Loop over all values for the name.
		// 	for _, value := range values {
		// 		log.Printf("%s: %v", name, value)
		// 	}
		// }
		return nil
	}
}

func errorHandler() func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, req *http.Request, err error) {
		log.Printf("[WARN] %v \n", err)
	}
}

// ProxyRequestHandler handles the http request using proxy
func ProxyRequestHandler(proxy *httputil.ReverseProxy) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		proxy.ServeHTTP(w, r)
	}
}

func createAcceptedHeader() map[string][]string {
	headers := make(map[string][]string)
	acceptedHeaders := os.Getenv("ACCEPTED_HEADERS") // seperate each value by \n ACCEPTED_HEADERS=NAME:VALUE1\nVALUE2
	strs := strings.Split(acceptedHeaders, ":")
	if len(strs) == 2 {
		key := strings.TrimSpace(strs[0])
		values := strings.Split(strings.TrimSpace(strs[1]), "\n")
		headers[key] = values
	}
	return headers
}

func main() {
	targetUrl := os.Getenv("TARGET_URL")
	proxyPort := os.Getenv("PROXY_PORT")
	if proxyPort == "" {
		proxyPort = "8080"
	}
	// initialize a reverse proxy and pass the actual backend server url here
	log.Printf("target url [%s]", targetUrl)
	proxy, err := NewProxy(targetUrl, createAcceptedHeader())
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

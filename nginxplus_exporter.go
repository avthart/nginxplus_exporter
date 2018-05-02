package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"bufio"
	"encoding/json"
	"github.com/avthart/nginxplus_exporter/nginx"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/log"
	"github.com/prometheus/common/version"
	"gopkg.in/alecthomas/kingpin.v2"
)

var (
	upstreamLabelNames   = []string{"upstream", "server"}
	serverZoneLabelNames = []string{"zone"}
)

// Exporter collects NGINX Plus status from the given URI and exports them using
// the prometheus metrics package.
type Exporter struct {
	URI   string
	mutex sync.RWMutex
	fetch func() (io.ReadCloser, error)

	up                           prometheus.Gauge
	totalScrapes, scrapeFailures prometheus.Counter
	serverMetrics                map[string]*prometheus.Desc
	httpServerZonesMetrics       map[string]*prometheus.Desc
	httpUpstreamMetrics          map[string]*prometheus.Desc
	streamServerZonesMetrics     map[string]*prometheus.Desc
	streamUpstreamMetrics        map[string]*prometheus.Desc
}

// NewExporter returns an initialized Exporter.
func NewExporter(uri string, sslVerify bool, timeout time.Duration, namespace string) (*Exporter, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}

	var fetch func() (io.ReadCloser, error)

	switch u.Scheme {
	case "http", "https", "file":
		fetch = fetchHTTP(uri, sslVerify, timeout)
	default:
		return nil, fmt.Errorf("unsupported scheme: %q", u.Scheme)
	}

	return &Exporter{
		URI:   uri,
		fetch: fetch,
		up: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "up",
			Help:      "Was the last scrape of NGINX Plus successful.",
		}),
		totalScrapes: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "exporter_total_scrapes",
			Help:      "Current total NGINX Plus scrapes.",
		}),
		scrapeFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "exporter_scrape_failures",
			Help:      "Number of errors while scraping metrics.",
		}),
		serverMetrics: map[string]*prometheus.Desc{
			"connections":  		 newServerMetric(namespace, "connections", "The total number of accepted, dropped, active or idle client connections.", []string{ "status"}),
			"ssl_handshakes":        newServerMetric(namespace, "ssl_handshakes", "The total number of successful SSL handshakes.", nil),
			"ssl_handshakes_failed": newServerMetric(namespace, "ssl_handshakes_failed", "The total number of failed SSL handshakes.", nil),
			"ssl_session_reuses":    newServerMetric(namespace, "ssl_session_reuses", "The total number of session reuses during SSL handshake.", nil),
			"requests_total":        newServerMetric(namespace, "requests_total", "The total number of client requests.", nil),
			"requests":              newServerMetric(namespace, "requests", "The current number of client requests.", nil),
			"processes_respawned":   newServerMetric(namespace, "processes_respawned", "The total number of abnormally terminated and respawned child processes.", nil),
		},
		httpServerZonesMetrics: map[string]*prometheus.Desc{
			"discarded":       	newHttpServerZoneMetric(namespace, "discarded", "The total number of requests completed without sending a response.", []string{"zone"}),
			"processing":      	newHttpServerZoneMetric(namespace, "processing", "The number of client requests that are currently being processed.", []string{"zone"}),
			"received":        	newHttpServerZoneMetric(namespace, "bytes_received", "The total number of bytes received from clients.", []string{"zone"}),
			"requests":        	newHttpServerZoneMetric(namespace, "requests", "The total number of client requests received from clients.", []string{"zone"}),
			"sent":            	newHttpServerZoneMetric(namespace, "bytes_sent", "The total number of bytes sent to clients.", []string{"zone"}),
			"responses":   		newHttpServerZoneMetric(namespace, "responses", "The number of responses with status codes 1xx, 2xx, 3xx, 4xx, and 5xx.",[]string{"zone", "code" }),
			"responses_total": 	newHttpServerZoneMetric(namespace, "responses_total", "The total number of responses sent to clients.", []string{"zone"}),
		},
		httpUpstreamMetrics: map[string]*prometheus.Desc{
			"up":                      newHttpUpstreamMetric(namespace, "up", "Indicating whether the server is up.", []string{ "upstream", "server" }),
			"state":         			newHttpUpstreamMetric(namespace, "state", "Current state, which may be one of “up”, “down”, “draining”, “unavail”, “checking”, or “unhealthy”.", []string{ "upstream", "server", "state" }),
			"backup":                  newHttpUpstreamMetric(namespace, "backup", "Indicating whether the server is a backup server.", []string{ "upstream", "server" }),
			"weight":                  newHttpUpstreamMetric(namespace, "weight", "Weight of the server.", []string{ "upstream", "server" }),
			"active":                  newHttpUpstreamMetric(namespace, "active", "The current number of active connections.", []string{ "upstream", "server" }),
			"max_conns":               newHttpUpstreamMetric(namespace, "max_conns", "The max_conns limit for the server.", []string{ "upstream", "server" }),
			"fails":                   newHttpUpstreamMetric(namespace, "fails", "The total number of unsuccessful attempts to communicate with the server.", []string{ "upstream", "server" }),
			"unavail":                 newHttpUpstreamMetric(namespace, "unavail", "How many times the server became unavailable for client requests (state “unavail”) due to the number of unsuccessful attempts reaching the max_fails threshold.", []string{ "upstream", "server" }),
			"downtime":                newHttpUpstreamMetric(namespace, "downtime", "Total time the server was in the “unavail”, “checking”, and “unhealthy” states.", []string{ "upstream", "server" }),
			"header_time":             newHttpUpstreamMetric(namespace, "header_time", "The average time to get the response header from the server.", []string{ "upstream", "server" }),
			"response_time":           newHttpUpstreamMetric(namespace, "response_time", "The average time to get the full response from the server.", []string{ "upstream", "server" }),
			"received":                newHttpUpstreamMetric(namespace, "bytes_received", "The total number of bytes received from clients.", []string{ "upstream", "server" }),
			"requests":                newHttpUpstreamMetric(namespace, "requests", "The total number of client connections forwarded to this server.", []string{ "upstream", "server" }),
			"sent":                    newHttpUpstreamMetric(namespace, "bytes_sent", "The total number of bytes sent to clients.", []string{ "upstream", "server" }),
			"healthcheck_checks":      newHttpUpstreamMetric(namespace, "healthcheck_checks", "The total number of health check requests made.", []string{ "upstream", "server" }),
			"healthcheck_fails":       newHttpUpstreamMetric(namespace, "healthcheck_fails", "The number of failed health checks.", []string{ "upstream", "server" }),
			"healthcheck_unhealthy":   newHttpUpstreamMetric(namespace, "healthcheck_unhealthy", "How many times the server became unhealthy (state “unhealthy”).",  []string{ "upstream", "server" }),
			"healthcheck_last_passed": newHttpUpstreamMetric(namespace, "healthcheck_last_passed", "Indicating if the last health check request was successful and passed tests.",  []string{ "upstream", "server" }),
			"responses":   			 	newHttpUpstreamMetric(namespace, "responses", "The number of responses with status codes 1xx, 2xx, 3xx, 4xx, and 5xx.",[]string{ "upstream", "server", "code" }),
			"responses_total":         newHttpUpstreamMetric(namespace, "responses_total", "The total number of responses sent to clients.", []string{ "upstream", "server" }),
			//"queue_size":   			newHttpUpstreamMetric("queue_size", "The current number of requests in the queue.", nil),
			//"queue_max_size":   		newHttpUpstreamMetric("queue_max_size", "The maximum number of requests that can be in the queue at the same time.", nil),
			//"queue_overflows": 			newHttpUpstreamMetric("queue_overflows", "The total number of requests rejected due to the queue overflow.", nil),

		},
		streamUpstreamMetrics: map[string]*prometheus.Desc{
			"up":                      newStreamUpstreamMetric(namespace, "up", "Indicating whether the server is up.", []string{ "upstream", "server" }),
			"state":                	newStreamUpstreamMetric(namespace, "state", "Current state, which may be one of “up”, “down”, “unavail”, “checking”, or “unhealthy”.", []string{ "upstream", "server", "state" }),
			"backup":                  newStreamUpstreamMetric(namespace, "backup", "Indicating whether the server is a backup server.", []string{ "upstream", "server" }),
			"weight":                  newStreamUpstreamMetric(namespace, "weight", "Weight of the server.", []string{ "upstream", "server" }),
			"active":                  newStreamUpstreamMetric(namespace, "active", "The current number of active connections.", []string{ "upstream", "server" }),
			"max_conns":               newStreamUpstreamMetric(namespace, "max_conns", "The max_conns limit for the server.", []string{ "upstream", "server" }),
			"fails":                   newStreamUpstreamMetric(namespace, "fails", "The total number of unsuccessful attempts to communicate with the server.", []string{ "upstream", "server" }),
			"unavail":                 newStreamUpstreamMetric(namespace, "unavail", "How many times the server became unavailable for client requests (state “unavail”) due to the number of unsuccessful attempts reaching the max_fails threshold.", []string{ "upstream", "server" }),
			"downtime":                newStreamUpstreamMetric(namespace, "downtime", "Total time the server was in the “unavail”, “checking”, and “unhealthy” states.", []string{ "upstream", "server" }),
			"connect_time":            newStreamUpstreamMetric(namespace, "connect_time", "The average time to connect to the upstream server. ", []string{ "upstream", "server" }),
			"first_byte_time":         newStreamUpstreamMetric(namespace, "first_byte_time", "The average time to connect to the upstream server. ", []string{ "upstream", "server" }),
			"response_time":           newStreamUpstreamMetric(namespace, "response_time", "The average time to get the full response from the server.", []string{ "upstream", "server" }),
			"received":                newStreamUpstreamMetric(namespace, "bytes_received", "The total number of bytes received from clients.", []string{ "upstream", "server" }),
			"connections":             newStreamUpstreamMetric(namespace, "connections", "The total number of client connections forwarded to this server.", []string{ "upstream", "server" }),
			"sent":                    newStreamUpstreamMetric(namespace, "bytes_sent", "The total number of bytes sent to clients.", []string{ "upstream", "server" }),
			"healthcheck_checks":      newStreamUpstreamMetric(namespace, "healthcheck_checks", "The total number of health check requests made.", []string{ "upstream", "server" }),
			"healthcheck_fails":       newStreamUpstreamMetric(namespace, "healthcheck_fails", "The number of failed health checks.", []string{ "upstream", "server" }),
			"healthcheck_unhealthy":   newStreamUpstreamMetric(namespace, "healthcheck_unhealthy", "How many times the server became unhealthy (state “unhealthy”).", []string{ "upstream", "server" }),
			"healthcheck_last_passed": newStreamUpstreamMetric(namespace, "healthcheck_last_passed", "Indicating if the last health check request was successful and passed tests.", []string{ "upstream", "server" }),
		},
		streamServerZonesMetrics: map[string]*prometheus.Desc{
			"discarded":      newStreamServerZoneMetric(namespace, "discarded", "The total number of connections completed without sending a response.", []string{ "zone" }),
			"processing":     newStreamServerZoneMetric(namespace, "processing", "The number of client connections that are currently being processed.",  []string{ "zone" }),
			"received":       newStreamServerZoneMetric(namespace, "bytes_received", "The total number of bytes received from clients.",  []string{ "zone" }),
			"connections":    newStreamServerZoneMetric(namespace, "connections", "The total number of connections accepted from clients.",  []string{ "zone" }),
			"sent":           newStreamServerZoneMetric(namespace, "bytes_sent", "The total number of bytes sent to clients.",  []string{ "zone" }),
			"sessions":   newStreamServerZoneMetric(namespace, "sessions", "The number of sessions completed with status codes 1xx, 2xx, 3xx, 4xx, and 5xx.",  []string{ "zone", "code" }),
			"sessions_total": newStreamServerZoneMetric(namespace, "sessions_total", "The total number of completed client sessions.",  []string{ "zone" }),
		},
	}, nil
}

func fetchHTTP(uri string, sslVerify bool, timeout time.Duration) func() (io.ReadCloser, error) {
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: !sslVerify}}
	client := http.Client{
		Timeout:   timeout,
		Transport: tr,
	}

	return func() (io.ReadCloser, error) {
		resp, err := client.Get(uri)
		if err != nil {
			return nil, err
		}
		if !(resp.StatusCode >= 200 && resp.StatusCode < 300) {
			resp.Body.Close()
			return nil, fmt.Errorf("HTTP status %d", resp.StatusCode)
		}
		return resp.Body, nil
	}
}

// Describe describes all the metrics ever exported by the NGINX Plus exporter. It
// implements prometheus.Collector.
func (e *Exporter) Describe(ch chan<- *prometheus.Desc) {
	for _, m := range e.httpServerZonesMetrics {
		ch <- m
	}
	for _, m := range e.httpUpstreamMetrics {
		ch <- m
	}
	for _, m := range e.streamServerZonesMetrics {
		ch <- m
	}
	for _, m := range e.streamUpstreamMetrics {
		ch <- m
	}
	for _, m := range e.serverMetrics {
		ch <- m
	}
	ch <- e.up.Desc()
	ch <- e.totalScrapes.Desc()
	ch <- e.scrapeFailures.Desc()
}

// Collect fetches the stats from configured NGINX Plus API and delivers them
// as Prometheus metrics. It implements prometheus.Collector.
func (e *Exporter) Collect(ch chan<- prometheus.Metric) {
	e.mutex.Lock() // To protect metrics from concurrent collects.
	defer e.mutex.Unlock()

	e.totalScrapes.Inc()

	body, err := e.fetch()
	if err != nil {
		e.up.Set(0)
		e.scrapeFailures.Inc()
		log.Errorf("Can't scrape NGINX Plus API: %v", err)
		return
	}

	dec := json.NewDecoder(bufio.NewReader(body))
	status := &nginx.Status{}
	if err := dec.Decode(status); err != nil {
		e.up.Set(0)
		e.scrapeFailures.Inc()
		log.Errorf("Can't parse JSON: %v", err)
	}
	scrapeConnections(status.Connections, e.serverMetrics, ch)
	scrapeSsl(status.Ssl, e.serverMetrics, ch)
	scrapeRequests(status.Requests, e.serverMetrics, ch)
	scrapeHttpServerZones(status.ServerZones, e.httpServerZonesMetrics, ch)
	scrapeHttpUpstreams(status.Upstreams, e.httpUpstreamMetrics, ch)
	scrapeTcpServerZones(status.Stream, e.streamServerZonesMetrics, ch)
	scrapeTcpUpstreams(status.Stream, e.streamUpstreamMetrics, ch)

	defer body.Close()

	e.up.Set(1)
	e.totalScrapes.Inc()
}

func scrapeHttpUpstreams(upstreams nginx.Upstreams, metrics map[string]*prometheus.Desc, ch chan<- prometheus.Metric) {
	for upstreamName, upstream := range upstreams {
		for _, peer := range upstream.Peers {
			ch <- prometheus.MustNewConstMetric(metrics["received"], prometheus.CounterValue, float64(peer.Received), upstreamName, peer.Server, )
			ch <- prometheus.MustNewConstMetric(metrics["sent"], prometheus.CounterValue, float64(peer.Sent), upstreamName, peer.Server, )
			ch <- prometheus.MustNewConstMetric(metrics["fails"], prometheus.CounterValue, float64(peer.Fails), upstreamName, peer.Server, )
			ch <- prometheus.MustNewConstMetric(metrics["requests"], prometheus.CounterValue, float64(peer.Requests), upstreamName, peer.Server, )
			ch <- prometheus.MustNewConstMetric(metrics["active"], prometheus.GaugeValue, float64(peer.Active), upstreamName, peer.Server, )
			ch <- prometheus.MustNewConstMetric(metrics["max_conns"], prometheus.GaugeValue, float64(peer.MaxConns), upstreamName, peer.Server, )
			ch <- prometheus.MustNewConstMetric(metrics["unavail"], prometheus.CounterValue, float64(peer.Unavail), upstreamName, peer.Server, )
			ch <- prometheus.MustNewConstMetric(metrics["weight"], prometheus.GaugeValue, float64(peer.Weight), upstreamName, peer.Server, )
			ch <- prometheus.MustNewConstMetric(metrics["header_time"], prometheus.GaugeValue, float64(peer.HeaderTime), upstreamName, peer.Server, )
			ch <- prometheus.MustNewConstMetric(metrics["response_time"], prometheus.GaugeValue, float64(peer.ResponseTime), upstreamName, peer.Server, )

			ch <- prometheus.MustNewConstMetric(metrics["responses"], prometheus.CounterValue, float64(peer.Responses.Responses1xx), upstreamName, peer.Server, "1xx" )
			ch <- prometheus.MustNewConstMetric(metrics["responses"], prometheus.CounterValue, float64(peer.Responses.Responses2xx), upstreamName, peer.Server, "2xx" )
			ch <- prometheus.MustNewConstMetric(metrics["responses"], prometheus.CounterValue, float64(peer.Responses.Responses3xx), upstreamName, peer.Server, "3xx" )
			ch <- prometheus.MustNewConstMetric(metrics["responses"], prometheus.CounterValue, float64(peer.Responses.Responses4xx), upstreamName, peer.Server, "4xx" )
			ch <- prometheus.MustNewConstMetric(metrics["responses"], prometheus.CounterValue, float64(peer.Responses.Responses5xx), upstreamName, peer.Server, "5xx" )
			ch <- prometheus.MustNewConstMetric(metrics["responses_total"], prometheus.CounterValue, float64(peer.Responses.Total), upstreamName, peer.Server, )

			ch <- prometheus.MustNewConstMetric(metrics["healthcheck_checks"], prometheus.CounterValue, float64(peer.HealthChecks.Checks), upstreamName, peer.Server, )
			ch <- prometheus.MustNewConstMetric(metrics["healthcheck_fails"], prometheus.CounterValue, float64(peer.HealthChecks.Fails), upstreamName, peer.Server, )
			ch <- prometheus.MustNewConstMetric(metrics["healthcheck_unhealthy"], prometheus.CounterValue, float64(peer.HealthChecks.Unhealthy), upstreamName, peer.Server, )
			if peer.HealthChecks.LastPassed {
				ch <- prometheus.MustNewConstMetric(metrics["healthcheck_last_passed"], prometheus.GaugeValue, 1, upstreamName, peer.Server, )
			} else {
				ch <- prometheus.MustNewConstMetric(metrics["healthcheck_last_passed"], prometheus.GaugeValue, 0, upstreamName, peer.Server, )
			}

			var up, down, draining, unavail, checking, unhealthy float64

			switch peer.State {
			case "up":
				up = 1
			case "down":
				down = 1
			case "draining":
				draining = 1
			case "unavail":
				unavail = 1
			case "checking":
				checking = 1
			case "unhealthy":
				unhealthy = 1
			}

			ch <- prometheus.MustNewConstMetric(metrics["up"], prometheus.GaugeValue, up, upstreamName, peer.Server, )
			ch <- prometheus.MustNewConstMetric(metrics["state"], prometheus.GaugeValue, up, upstreamName, peer.Server, "up" )
			ch <- prometheus.MustNewConstMetric(metrics["state"], prometheus.GaugeValue, down, upstreamName, peer.Server, "down" )
			ch <- prometheus.MustNewConstMetric(metrics["state"], prometheus.GaugeValue, draining, upstreamName, peer.Server, "draining" )
			ch <- prometheus.MustNewConstMetric(metrics["state"], prometheus.GaugeValue, unavail, upstreamName, peer.Server, "unavail" )
			ch <- prometheus.MustNewConstMetric(metrics["state"], prometheus.GaugeValue, checking, upstreamName, peer.Server, "checking" )
			ch <- prometheus.MustNewConstMetric(metrics["state"], prometheus.GaugeValue, unhealthy, upstreamName, peer.Server, "unhealthy" )
		}
	}
}

func scrapeHttpServerZones(zones nginx.ServerZones, metrics map[string]*prometheus.Desc, ch chan<- prometheus.Metric) {
	for zoneName, zone := range zones {
		zoneLabels := make(map[string]string)
		zoneLabels["zone"] = zoneName

		ch <- prometheus.MustNewConstMetric(metrics["received"], prometheus.CounterValue, float64(zone.Received), zoneName, )
		ch <- prometheus.MustNewConstMetric(metrics["sent"], prometheus.CounterValue, float64(zone.Sent), zoneName, )
		ch <- prometheus.MustNewConstMetric(metrics["processing"], prometheus.GaugeValue, float64(zone.Processing), zoneName, )
		ch <- prometheus.MustNewConstMetric(metrics["discarded"], prometheus.CounterValue, float64(zone.Discarded), zoneName, )
		ch <- prometheus.MustNewConstMetric(metrics["requests"], prometheus.CounterValue, float64(zone.Requests), zoneName, )

		ch <- prometheus.MustNewConstMetric(metrics["responses"], prometheus.CounterValue, float64(zone.Responses.Responses1xx), zoneName, "1xx" )
		ch <- prometheus.MustNewConstMetric(metrics["responses"], prometheus.CounterValue, float64(zone.Responses.Responses2xx), zoneName, "2xx" )
		ch <- prometheus.MustNewConstMetric(metrics["responses"], prometheus.CounterValue, float64(zone.Responses.Responses3xx), zoneName, "3xx" )
		ch <- prometheus.MustNewConstMetric(metrics["responses"], prometheus.CounterValue, float64(zone.Responses.Responses4xx), zoneName, "4xx" )
		ch <- prometheus.MustNewConstMetric(metrics["responses"], prometheus.CounterValue, float64(zone.Responses.Responses5xx), zoneName, "5xx" )
		ch <- prometheus.MustNewConstMetric(metrics["responses_total"], prometheus.CounterValue, float64(zone.Responses.Total), zoneName, )

	}
}

func scrapeTcpUpstreams(stream nginx.Stream, metrics map[string]*prometheus.Desc, ch chan<- prometheus.Metric) {
	for upstreamName, upstream := range stream.Upstreams {
		for _, peer := range upstream.Peers {
			ch <- prometheus.MustNewConstMetric(metrics["received"], prometheus.CounterValue, float64(peer.Received), upstreamName, peer.Server, )
			ch <- prometheus.MustNewConstMetric(metrics["sent"], prometheus.CounterValue, float64(peer.Sent), upstreamName, peer.Server, )
			ch <- prometheus.MustNewConstMetric(metrics["fails"], prometheus.CounterValue, float64(peer.Fails), upstreamName, peer.Server, )
			ch <- prometheus.MustNewConstMetric(metrics["connections"], prometheus.CounterValue, float64(peer.Connections), upstreamName, peer.Server, )
			ch <- prometheus.MustNewConstMetric(metrics["active"], prometheus.GaugeValue, float64(peer.Active), upstreamName, peer.Server, )
			ch <- prometheus.MustNewConstMetric(metrics["max_conns"], prometheus.GaugeValue, float64(peer.MaxConns), upstreamName, peer.Server, )
			ch <- prometheus.MustNewConstMetric(metrics["unavail"], prometheus.CounterValue, float64(peer.Unavail), upstreamName, peer.Server, )
			ch <- prometheus.MustNewConstMetric(metrics["weight"], prometheus.GaugeValue, float64(peer.Weight), upstreamName, peer.Server, )
			ch <- prometheus.MustNewConstMetric(metrics["connect_time"], prometheus.GaugeValue, float64(peer.ConnectTime), upstreamName, peer.Server, )
			ch <- prometheus.MustNewConstMetric(metrics["first_byte_time"], prometheus.GaugeValue, float64(peer.FirstByteTime), upstreamName, peer.Server, )
			ch <- prometheus.MustNewConstMetric(metrics["response_time"], prometheus.GaugeValue, float64(peer.ResponseTime), upstreamName, peer.Server, )

			ch <- prometheus.MustNewConstMetric(metrics["healthcheck_checks"], prometheus.CounterValue, float64(peer.HealthChecks.Checks), upstreamName, peer.Server, )
			ch <- prometheus.MustNewConstMetric(metrics["healthcheck_fails"], prometheus.CounterValue, float64(peer.HealthChecks.Fails), upstreamName, peer.Server, )
			ch <- prometheus.MustNewConstMetric(metrics["healthcheck_unhealthy"], prometheus.CounterValue, float64(peer.HealthChecks.Unhealthy), upstreamName, peer.Server, )
			if peer.HealthChecks.LastPassed {
				ch <- prometheus.MustNewConstMetric(metrics["healthcheck_last_passed"], prometheus.GaugeValue, 1, upstreamName, peer.Server, )
			} else {
				ch <- prometheus.MustNewConstMetric(metrics["healthcheck_last_passed"], prometheus.GaugeValue, 0, upstreamName, peer.Server, )
			}

			var up, down, unavail, checking, unhealthy float64

			switch peer.State {
			case "up":
				up = 1
			case "down":
				down = 1
			case "unavail":
				unavail = 1
			case "checking":
				checking = 1
			case "unhealthy":
				unhealthy = 1
			}

			ch <- prometheus.MustNewConstMetric(metrics["up"], prometheus.GaugeValue, up, upstreamName, peer.Server, )
			ch <- prometheus.MustNewConstMetric(metrics["state"], prometheus.GaugeValue, up, upstreamName, peer.Server, "up" )
			ch <- prometheus.MustNewConstMetric(metrics["state"], prometheus.GaugeValue, down, upstreamName, peer.Server, "down" )
			ch <- prometheus.MustNewConstMetric(metrics["state"], prometheus.GaugeValue, unavail, upstreamName, peer.Server, "unavail" )
			ch <- prometheus.MustNewConstMetric(metrics["state"], prometheus.GaugeValue, checking, upstreamName, peer.Server, "checking" )
			ch <- prometheus.MustNewConstMetric(metrics["state"], prometheus.GaugeValue, unhealthy, upstreamName, peer.Server, "unhealthy" )
		}
	}
}

func scrapeTcpServerZones(stream nginx.Stream, metrics map[string]*prometheus.Desc, ch chan<- prometheus.Metric) {
	for zoneName, zone := range stream.ServerZones {
		zoneLabels := make(map[string]string)
		zoneLabels["zone"] = zoneName

		ch <- prometheus.MustNewConstMetric(metrics["received"], prometheus.CounterValue, float64(zone.Received), zoneName, )
		ch <- prometheus.MustNewConstMetric(metrics["sent"], prometheus.CounterValue, float64(zone.Sent), zoneName, )
		ch <- prometheus.MustNewConstMetric(metrics["processing"], prometheus.GaugeValue, float64(zone.Processing), zoneName, )
		ch <- prometheus.MustNewConstMetric(metrics["discarded"], prometheus.CounterValue, float64(zone.Discarded), zoneName, )
		ch <- prometheus.MustNewConstMetric(metrics["connections"], prometheus.CounterValue, float64(zone.Connections), zoneName, )

		ch <- prometheus.MustNewConstMetric(metrics["sessions"], prometheus.CounterValue, float64(zone.Sessions.Sessions1xx), zoneName, "1xx" )
		ch <- prometheus.MustNewConstMetric(metrics["sessions"], prometheus.CounterValue, float64(zone.Sessions.Sessions2xx), zoneName, "2xx" )
		ch <- prometheus.MustNewConstMetric(metrics["sessions"], prometheus.CounterValue, float64(zone.Sessions.Sessions3xx), zoneName, "3xx" )
		ch <- prometheus.MustNewConstMetric(metrics["sessions"], prometheus.CounterValue, float64(zone.Sessions.Sessions4xx), zoneName, "4xx" )
		ch <- prometheus.MustNewConstMetric(metrics["sessions"], prometheus.CounterValue, float64(zone.Sessions.Sessions5xx), zoneName, "5xx" )
		ch <- prometheus.MustNewConstMetric(metrics["sessions_total"], prometheus.CounterValue, float64(zone.Sessions.Total), zoneName, )
	}
}

func scrapeSsl(ssl nginx.Ssl, metrics map[string]*prometheus.Desc, ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(metrics["ssl_handshakes"], prometheus.CounterValue, float64(ssl.Handshakes), )
	ch <- prometheus.MustNewConstMetric(metrics["ssl_handshakes_failed"], prometheus.CounterValue, float64(ssl.HandshakesFailed), )
	ch <- prometheus.MustNewConstMetric(metrics["ssl_session_reuses"], prometheus.CounterValue, float64(ssl.SessionReuses), )
}

func scrapeRequests(requests nginx.Requests, metrics map[string]*prometheus.Desc, ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(metrics["requests_total"], prometheus.CounterValue, float64(requests.Total), )
	ch <- prometheus.MustNewConstMetric(metrics["requests"], prometheus.GaugeValue, float64(requests.Current), )
}

func scrapeConnections(connections nginx.Connections, metrics map[string]*prometheus.Desc, ch chan<- prometheus.Metric) {
	ch <- prometheus.MustNewConstMetric(metrics["connections"], prometheus.GaugeValue, float64(connections.Accepted), "accepted")
	ch <- prometheus.MustNewConstMetric(metrics["connections"], prometheus.GaugeValue, float64(connections.Dropped), "dropped")
	ch <- prometheus.MustNewConstMetric(metrics["connections"], prometheus.GaugeValue, float64(connections.Active), "active")
	ch <- prometheus.MustNewConstMetric(metrics["connections"], prometheus.GaugeValue, float64(connections.Idle), "idle")
}

func newServerMetric(namespace string, metricName string, docString string, labels []string) *prometheus.Desc {
	return prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "server", metricName),
			docString, labels, nil,
	)
}

func newHttpServerZoneMetric(namespace string, metricName string, docString string, labels []string) *prometheus.Desc {
	return prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "http_server_zone", metricName),
			docString, labels, nil,
	)
}

func newHttpUpstreamMetric(namespace string, metricName string, docString string, labels []string) *prometheus.Desc {
	return prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "http_upstream_peer", metricName),
			       docString, labels, nil,
	)
}

func newStreamServerZoneMetric(namespace string, metricName string, docString string, labels []string) *prometheus.Desc {
	return prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "stream_server_zone", metricName),
			docString, labels, nil,
	)
}

func newStreamUpstreamMetric(namespace string, metricName string, docString string, labels []string) *prometheus.Desc {
	return prometheus.NewDesc(
		prometheus.BuildFQName(namespace, "stream_upstream_peer", metricName),
			docString, labels, nil,
	)
}

func main() {
	var (
		listenAddress      = kingpin.Flag("web.listen-address", "Address to listen on for web interface and telemetry.").Default(":9102").String()
		metricsPath        = kingpin.Flag("web.telemetry-path", "Path under which to expose metrics.").Default("/metrics").String()
		nginxPlusScrapeURI = kingpin.Flag("nginx.scrape-uri", "URI on which to scrape NGINX Plus Status API.").Default("http://localhost:1080/nginx_status").String()
		nginxPlusSSLVerify = kingpin.Flag("nginx.ssl-verify", "Flag that enables SSL certificate verification for the scrape URI.").Default("true").Bool()
		nginxPlusTimeout   = kingpin.Flag("nginx.timeout", "Timeout for trying to get stats from NGINX Plus Status API.").Default("5s").Duration()
		exporterNamespace  = kingpin.Flag("exporter.namespace", "Namespace for the prometheus metrics. Default is nginx").Default("nginx").String()
	)

	log.AddFlags(kingpin.CommandLine)
	kingpin.Version(version.Print("nginxplus_exporter"))
	kingpin.HelpFlag.Short('h')
	kingpin.Parse()

	log.Infoln("Starting nginxplus_exporter", version.Info())
	log.Infoln("Build context", version.BuildContext())

	exporter, err := NewExporter(*nginxPlusScrapeURI, *nginxPlusSSLVerify, *nginxPlusTimeout, *exporterNamespace)
	if err != nil {
		log.Fatal(err)
	}
	prometheus.MustRegister(exporter)
	prometheus.MustRegister(version.NewCollector("nginx_exporter"))

	log.Infoln("Listening on", *listenAddress)
	http.Handle(*metricsPath, prometheus.Handler())
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
             <head><title>NGINX Plus Exporter</title></head>
             <body>
             <h1>NGINX Plus Exporter</h1>
             <p><a href='` + *metricsPath + `'>Metrics</a></p>
             </body>
             </html>`))
	})
	log.Fatal(http.ListenAndServe(*listenAddress, nil))
}

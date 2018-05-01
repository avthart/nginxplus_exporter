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
	serverMetrics                map[string]*prometheus.GaugeVec
	httpServerZonesMetrics       map[string]*prometheus.GaugeVec
	httpUpstreamMetrics          map[string]*prometheus.GaugeVec
	streamServerZonesMetrics     map[string]*prometheus.GaugeVec
	streamUpstreamMetrics        map[string]*prometheus.GaugeVec
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
		serverMetrics: map[string]*prometheus.GaugeVec{
			"connections_accepted":  newServerMetric(namespace,"connections_accepted", "The total number of accepted client connections.", nil),
			"connections_dropped":   newServerMetric(namespace,"connections_dropped", "The total number of dropped client connections.", nil),
			"connections_active":    newServerMetric(namespace,"connections_active", "The current number of active client connections.", nil),
			"connections_idle":      newServerMetric(namespace,"connections_idle", "The current number of idle client connections.", nil),
			"ssl_handshakes":        newServerMetric(namespace,"ssl_handshakes", "The total number of successful SSL handshakes.", nil),
			"ssl_handshakes_failed": newServerMetric(namespace,"ssl_handshakes_failed", "The total number of failed SSL handshakes.", nil),
			"ssl_session_reuses":    newServerMetric(namespace,"ssl_session_reuses", "The total number of session reuses during SSL handshake.", nil),
			"requests_total":        newServerMetric(namespace,"requests_total", "The total number of client requests.", nil),
			"requests":              newServerMetric(namespace,"requests", "The current number of client requests.", nil),
			"processes_respawned":   newServerMetric(namespace,"processes_respawned", "The total number of abnormally terminated and respawned child processes.", nil),
		},
		httpServerZonesMetrics: map[string]*prometheus.GaugeVec{
			"discarded":       newHttpServerZoneMetric(namespace,"discarded", "The total number of requests completed without sending a response.", nil),
			"processing":      newHttpServerZoneMetric(namespace,"processing", "The number of client requests that are currently being processed.", nil),
			"received":        newHttpServerZoneMetric(namespace,"bytes_received", "The total number of bytes received from clients.", nil),
			"requests":        newHttpServerZoneMetric(namespace,"requests", "The total number of client requests received from clients.", nil),
			"sent":            newHttpServerZoneMetric(namespace,"bytes_sent", "The total number of bytes sent to clients.", nil),
			"responses_1xx":   newHttpServerZoneMetric(namespace,"responses", "The number of responses with status codes 1xx, 2xx, 3xx, 4xx, and 5xx.", prometheus.Labels{"code": "1xx"}),
			"responses_2xx":   newHttpServerZoneMetric(namespace,"responses", "The number of responses with status codes 1xx, 2xx, 3xx, 4xx, and 5xx.", prometheus.Labels{"code": "2xx"}),
			"responses_3xx":   newHttpServerZoneMetric(namespace,"responses", "The number of responses with status codes 1xx, 2xx, 3xx, 4xx, and 5xx.", prometheus.Labels{"code": "3xx"}),
			"responses_4xx":   newHttpServerZoneMetric(namespace,"responses", "The number of responses with status codes 1xx, 2xx, 3xx, 4xx, and 5xx.", prometheus.Labels{"code": "4xx"}),
			"responses_5xx":   newHttpServerZoneMetric(namespace,"responses", "The number of responses with status codes 1xx, 2xx, 3xx, 4xx, and 5xx.", prometheus.Labels{"code": "5xx"}),
			"responses_total": newHttpServerZoneMetric(namespace,"responses_total", "The total number of responses sent to clients.", nil),
		},
		httpUpstreamMetrics: map[string]*prometheus.GaugeVec{
			"up":                      newHttpUpstreamMetric(namespace,"up", "Indicating whether the server is up.", nil),
			"state_up":                newHttpUpstreamMetric(namespace,"state", "Current state, which may be one of “up”, “down”, “draining”, “unavail”, “checking”, or “unhealthy”.", prometheus.Labels{"state": "up"}),
			"state_down":              newHttpUpstreamMetric(namespace,"state", "Current state, which may be one of “up”, “down”, “draining”, “unavail”, “checking”, or “unhealthy”.", prometheus.Labels{"state": "down"}),
			"state_draining":          newHttpUpstreamMetric(namespace,"state", "Current state, which may be one of “up”, “down”, “draining”, “unavail”, “checking”, or “unhealthy”.", prometheus.Labels{"state": "draining"}),
			"state_unavail":           newHttpUpstreamMetric(namespace,"state", "Current state, which may be one of “up”, “down”, “draining”, “unavail”, “checking”, or “unhealthy”.", prometheus.Labels{"state": "unavail"}),
			"state_checking":          newHttpUpstreamMetric(namespace,"state", "Current state, which may be one of “up”, “down”, “draining”, “unavail”, “checking”, or “unhealthy”.", prometheus.Labels{"state": "checking"}),
			"state_unhealthy":         newHttpUpstreamMetric(namespace,"state", "Current state, which may be one of “up”, “down”, “draining”, “unavail”, “checking”, or “unhealthy”.", prometheus.Labels{"state": "unhealthy"}),
			"backup":                  newHttpUpstreamMetric(namespace,"backup", "Indicating whether the server is a backup server.", nil),
			"weight":                  newHttpUpstreamMetric(namespace,"active", "The current number of active connections.", nil),
			"max_conns":               newHttpUpstreamMetric(namespace,"max_conns", "The max_conns limit for the server.", nil),
			"fails":                   newHttpUpstreamMetric(namespace,"fails", "The total number of unsuccessful attempts to communicate with the server.", nil),
			"unavail":                 newHttpUpstreamMetric(namespace,"unavail", "How many times the server became unavailable for client requests (state “unavail”) due to the number of unsuccessful attempts reaching the max_fails threshold.", nil),
			"downtime":                newHttpUpstreamMetric(namespace,"downtime", "Total time the server was in the “unavail”, “checking”, and “unhealthy” states.", nil),
			"header_time":             newHttpUpstreamMetric(namespace,"header_time", "The average time to get the response header from the server.", nil),
			"response_time":           newHttpUpstreamMetric(namespace,"response_time", "The average time to get the full response from the server.", nil),
			"received":                newHttpUpstreamMetric(namespace,"bytes_received", "The total number of bytes received from clients.", nil),
			"requests":                newHttpUpstreamMetric(namespace,"requests", "The total number of client connections forwarded to this server.", nil),
			"sent":                    newHttpUpstreamMetric(namespace,"bytes_sent", "The total number of bytes sent to clients.", nil),
			"healthcheck_checks":      newHttpUpstreamMetric(namespace,"healthcheck_checks", "The total number of health check requests made.", nil),
			"healthcheck_fails":       newHttpUpstreamMetric(namespace,"healthcheck_fails", "The number of failed health checks.", nil),
			"healthcheck_unhealthy":   newHttpUpstreamMetric(namespace,"healthcheck_unhealthy", "How many times the server became unhealthy (state “unhealthy”).", nil),
			"healthcheck_last_passed": newHttpUpstreamMetric(namespace,"healthcheck_last_passed", "Indicating if the last health check request was successful and passed tests.", nil),
			"responses_1xx":           newHttpUpstreamMetric(namespace,"responses", "The number of responses with status codes 1xx, 2xx, 3xx, 4xx, and 5xx.", prometheus.Labels{"code": "1xx"}),
			"responses_2xx":           newHttpUpstreamMetric(namespace,"responses", "The number of responses with status codes 1xx, 2xx, 3xx, 4xx, and 5xx.", prometheus.Labels{"code": "2xx"}),
			"responses_3xx":           newHttpUpstreamMetric(namespace,"responses", "The number of responses with status codes 1xx, 2xx, 3xx, 4xx, and 5xx.", prometheus.Labels{"code": "3xx"}),
			"responses_4xx":           newHttpUpstreamMetric(namespace,"responses", "The number of responses with status codes 1xx, 2xx, 3xx, 4xx, and 5xx.", prometheus.Labels{"code": "4xx"}),
			"responses_5xx":           newHttpUpstreamMetric(namespace,"responses", "The number of responses with status codes 1xx, 2xx, 3xx, 4xx, and 5xx.", prometheus.Labels{"code": "5xx"}),
			"responses_total":         newHttpUpstreamMetric(namespace,"responses_total", "The total number of responses sent to clients.", nil),
			//"queue_size":   			newHttpUpstreamMetric("queue_size", "The current number of requests in the queue.", nil),
			//"queue_max_size":   		newHttpUpstreamMetric("queue_max_size", "The maximum number of requests that can be in the queue at the same time.", nil),
			//"queue_overflows": 			newHttpUpstreamMetric("queue_overflows", "The total number of requests rejected due to the queue overflow.", nil),

		},
		streamUpstreamMetrics: map[string]*prometheus.GaugeVec{
			"up":                      newStreamUpstreamMetric(namespace,"up", "Indicating whether the server is up.", nil),
			"state_up":                newStreamUpstreamMetric(namespace,"state", "Current state, which may be one of “up”, “down”, “unavail”, “checking”, or “unhealthy”.", prometheus.Labels{"state": "up"}),
			"state_down":              newStreamUpstreamMetric(namespace,"state", "Current state, which may be one of “up”, “down”, “unavail”, “checking”, or “unhealthy”.", prometheus.Labels{"state": "down"}),
			"state_unavail":           newStreamUpstreamMetric(namespace,"state", "Current state, which may be one of “up”, “down”, “unavail”, “checking”, or “unhealthy”.", prometheus.Labels{"state": "unavail"}),
			"state_checking":          newStreamUpstreamMetric(namespace,"state", "Current state, which may be one of “up”, “down”, “unavail”, “checking”, or “unhealthy”.", prometheus.Labels{"state": "checking"}),
			"state_unhealthy":         newStreamUpstreamMetric(namespace,"state", "Current state, which may be one of “up”, “down”, “unavail”, “checking”, or “unhealthy”.", prometheus.Labels{"state": "unhealthy"}),
			"backup":                  newStreamUpstreamMetric(namespace,"backup", "Indicating whether the server is a backup server.", nil),
			"weight":                  newStreamUpstreamMetric(namespace,"weight", "Weight of the server.", nil),
			"active":                  newStreamUpstreamMetric(namespace,"active", "The current number of active connections.", nil),
			"max_conns":               newStreamUpstreamMetric(namespace,"max_conns", "The max_conns limit for the server.", nil),
			"fails":                   newStreamUpstreamMetric(namespace,"fails", "The total number of unsuccessful attempts to communicate with the server.", nil),
			"unavail":                 newStreamUpstreamMetric(namespace,"unavail", "How many times the server became unavailable for client requests (state “unavail”) due to the number of unsuccessful attempts reaching the max_fails threshold.", nil),
			"downtime":                newStreamUpstreamMetric(namespace,"downtime", "Total time the server was in the “unavail”, “checking”, and “unhealthy” states.", nil),
			"connect_time":            newStreamUpstreamMetric(namespace,"connect_time", "The average time to connect to the upstream server. ", nil),
			"first_byte_time":         newStreamUpstreamMetric(namespace,"first_byte_time", "The average time to connect to the upstream server. ", nil),
			"response_time":           newStreamUpstreamMetric(namespace,"response_time", "The average time to get the full response from the server.", nil),
			"received":                newStreamUpstreamMetric(namespace,"bytes_received", "The total number of bytes received from clients.", nil),
			"connections":             newStreamUpstreamMetric(namespace,"connections", "The total number of client connections forwarded to this server.", nil),
			"sent":                    newStreamUpstreamMetric(namespace,"bytes_sent", "The total number of bytes sent to clients.", nil),
			"healthcheck_checks":      newStreamUpstreamMetric(namespace,"healthcheck_checks", "The total number of health check requests made.", nil),
			"healthcheck_fails":       newStreamUpstreamMetric(namespace,"healthcheck_fails", "The number of failed health checks.", nil),
			"healthcheck_unhealthy":   newStreamUpstreamMetric(namespace,"healthcheck_unhealthy", "How many times the server became unhealthy (state “unhealthy”).", nil),
			"healthcheck_last_passed": newStreamUpstreamMetric(namespace,"healthcheck_last_passed", "Indicating if the last health check request was successful and passed tests.", nil),
		},
		streamServerZonesMetrics: map[string]*prometheus.GaugeVec{
			"discarded":      newStreamServerZoneMetric(namespace,"discarded", "The total number of connections completed without sending a response.", nil),
			"processing":     newStreamServerZoneMetric(namespace,"processing", "The number of client connections that are currently being processed.", nil),
			"received":       newStreamServerZoneMetric(namespace,"bytes_received", "The total number of bytes received from clients.", nil),
			"connections":    newStreamServerZoneMetric(namespace,"connections", "The total number of connections accepted from clients.", nil),
			"sent":           newStreamServerZoneMetric(namespace,"bytes_sent", "The total number of bytes sent to clients.", nil),
			"sessions_1xx":   newStreamServerZoneMetric(namespace,"sessions", "The number of sessions completed with status codes 1xx, 2xx, 3xx, 4xx, and 5xx.", prometheus.Labels{"code": "1xx"}),
			"sessions_2xx":   newStreamServerZoneMetric(namespace,"sessions", "The number of sessions completed with status codes 1xx, 2xx, 3xx, 4xx, and 5xx.", prometheus.Labels{"code": "2xx"}),
			"sessions_4xx":   newStreamServerZoneMetric(namespace,"sessions", "The number of sessions completed with status codes 1xx, 2xx, 3xx, 4xx, and 5xx.", prometheus.Labels{"code": "4xx"}),
			"sessions_5xx":   newStreamServerZoneMetric(namespace,"sessions", "The number of sessions completed with status codes 1xx, 2xx, 3xx, 4xx, and 5xx.", prometheus.Labels{"code": "5xx"}),
			"sessions_total": newStreamServerZoneMetric(namespace,"sessions_total", "The total number of completed client sessions.", nil),
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
		m.Describe(ch)
	}
	for _, m := range e.serverMetrics {
		m.Describe(ch)
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

	e.resetMetrics()
	e.scrape()

	ch <- e.up
	ch <- e.totalScrapes
	ch <- e.scrapeFailures
	e.collectMetrics(ch)
}

func (e *Exporter) scrape() {
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
	scrapeConnections(status.Connections, e.serverMetrics)
	scrapeSsl(status.Ssl, e.serverMetrics)
	scrapeRequests(status.Requests, e.serverMetrics)
	scrapeHttpServerZones(status.ServerZones, e.httpServerZonesMetrics)
	scrapeHttpUpstreams(status.Upstreams, e.httpUpstreamMetrics)
	scrapeTcpServerZones(status.Stream, e.streamServerZonesMetrics)
	scrapeTcpUpstreams(status.Stream, e.streamUpstreamMetrics)

	defer body.Close()

	e.up.Set(1)
	e.totalScrapes.Inc()
}

func scrapeHttpUpstreams(upstreams nginx.Upstreams, metrics map[string]*prometheus.GaugeVec) {
	for upstreamName, upstream := range upstreams {
		zoneLabels := make(map[string]string)
		zoneLabels["upstream"] = upstreamName
		for _, peer := range upstream.Peers {
			zoneLabels["server"] = peer.Server

			metrics["received"].With(zoneLabels).Set(float64(peer.Received))
			metrics["fails"].With(zoneLabels).Set(float64(peer.Fails))
			metrics["sent"].With(zoneLabels).Set(float64(peer.Received))
			metrics["requests"].With(zoneLabels).Set(float64(peer.Requests))
			metrics["active"].With(zoneLabels).Set(float64(peer.Active))
			metrics["max_conns"].With(zoneLabels).Set(float64(peer.MaxConns))
			metrics["unavail"].With(zoneLabels).Set(float64(peer.Unavail))
			metrics["weight"].With(zoneLabels).Set(float64(peer.Weight))

			metrics["header_time"].With(zoneLabels).Set(float64(peer.HeaderTime))
			metrics["response_time"].With(zoneLabels).Set(float64(peer.ResponseTime))

			metrics["responses_1xx"].With(zoneLabels).Set(float64(peer.Responses.Responses1xx))
			metrics["responses_2xx"].With(zoneLabels).Set(float64(peer.Responses.Responses2xx))
			metrics["responses_3xx"].With(zoneLabels).Set(float64(peer.Responses.Responses3xx))
			metrics["responses_4xx"].With(zoneLabels).Set(float64(peer.Responses.Responses4xx))
			metrics["responses_5xx"].With(zoneLabels).Set(float64(peer.Responses.Responses5xx))
			metrics["responses_total"].With(zoneLabels).Set(float64(peer.Responses.Total))

			metrics["healthcheck_checks"].With(zoneLabels).Set(float64(peer.HealthChecks.Checks))
			metrics["healthcheck_fails"].With(zoneLabels).Set(float64(peer.HealthChecks.Fails))
			metrics["healthcheck_unhealthy"].With(zoneLabels).Set(float64(peer.HealthChecks.Unhealthy))
			if peer.HealthChecks.LastPassed {
				metrics["healthcheck_last_passed"].With(zoneLabels).Set(1)
			} else {
				metrics["healthcheck_last_passed"].With(zoneLabels).Set(0)
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
			metrics["up"].With(zoneLabels).Set(up)
			metrics["state_up"].With(zoneLabels).Set(up)
			metrics["state_down"].With(zoneLabels).Set(down)
			metrics["state_draining"].With(zoneLabels).Set(draining)
			metrics["state_unavail"].With(zoneLabels).Set(unavail)
			metrics["state_checking"].With(zoneLabels).Set(checking)
			metrics["state_unhealthy"].With(zoneLabels).Set(unhealthy)
		}
	}
}

func scrapeHttpServerZones(zones nginx.ServerZones, metrics map[string]*prometheus.GaugeVec) {
	for zoneName, zone := range zones {
		zoneLabels := make(map[string]string)
		zoneLabels["zone"] = zoneName

		metrics["received"].With(zoneLabels).Set(float64(zone.Received))
		metrics["processing"].With(zoneLabels).Set(float64(zone.Processing))
		metrics["sent"].With(zoneLabels).Set(float64(zone.Sent))
		metrics["requests"].With(zoneLabels).Set(float64(zone.Requests))
		metrics["discarded"].With(zoneLabels).Set(float64(zone.Discarded))

		metrics["responses_1xx"].With(zoneLabels).Set(float64(zone.Responses.Responses1xx))
		metrics["responses_2xx"].With(zoneLabels).Set(float64(zone.Responses.Responses2xx))
		metrics["responses_3xx"].With(zoneLabels).Set(float64(zone.Responses.Responses3xx))
		metrics["responses_4xx"].With(zoneLabels).Set(float64(zone.Responses.Responses4xx))
		metrics["responses_5xx"].With(zoneLabels).Set(float64(zone.Responses.Responses5xx))
		metrics["responses_total"].With(zoneLabels).Set(float64(zone.Responses.Total))
	}
}

func scrapeTcpUpstreams(stream nginx.Stream, metrics map[string]*prometheus.GaugeVec) {
	for upstreamName, upstream := range stream.Upstreams {
		zoneLabels := make(map[string]string)
		zoneLabels["upstream"] = upstreamName
		for _, peer := range upstream.Peers {
			zoneLabels["server"] = peer.Server

			metrics["received"].With(zoneLabels).Set(float64(peer.Received))
			metrics["fails"].With(zoneLabels).Set(float64(peer.Fails))
			metrics["sent"].With(zoneLabels).Set(float64(peer.Received))
			metrics["connections"].With(zoneLabels).Set(float64(peer.Connections))
			metrics["active"].With(zoneLabels).Set(float64(peer.Active))
			metrics["max_conns"].With(zoneLabels).Set(float64(peer.MaxConns))
			metrics["unavail"].With(zoneLabels).Set(float64(peer.Unavail))
			metrics["weight"].With(zoneLabels).Set(float64(peer.Weight))

			metrics["connect_time"].With(zoneLabels).Set(float64(peer.ConnectTime))
			metrics["first_byte_time"].With(zoneLabels).Set(float64(peer.FirstByteTime))
			metrics["response_time"].With(zoneLabels).Set(float64(peer.ResponseTime))

			metrics["healthcheck_checks"].With(zoneLabels).Set(float64(peer.HealthChecks.Checks))
			metrics["healthcheck_fails"].With(zoneLabels).Set(float64(peer.HealthChecks.Fails))
			metrics["healthcheck_unhealthy"].With(zoneLabels).Set(float64(peer.HealthChecks.Unhealthy))
			if peer.HealthChecks.LastPassed {
				metrics["healthcheck_last_passed"].With(zoneLabels).Set(1)
			} else {
				metrics["healthcheck_last_passed"].With(zoneLabels).Set(0)
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
			metrics["up"].With(zoneLabels).Set(up)
			metrics["state_up"].With(zoneLabels).Set(up)
			metrics["state_down"].With(zoneLabels).Set(down)
			metrics["state_unavail"].With(zoneLabels).Set(unavail)
			metrics["state_checking"].With(zoneLabels).Set(checking)
			metrics["state_unhealthy"].With(zoneLabels).Set(unhealthy)
		}
	}
}

func scrapeTcpServerZones(stream nginx.Stream, metrics map[string]*prometheus.GaugeVec) {
	for zoneName, zone := range stream.ServerZones {
		zoneLabels := make(map[string]string)
		zoneLabels["zone"] = zoneName

		metrics["received"].With(zoneLabels).Set(float64(zone.Received))
		metrics["processing"].With(zoneLabels).Set(float64(zone.Processing))
		metrics["sent"].With(zoneLabels).Set(float64(zone.Sent))
		metrics["connections"].With(zoneLabels).Set(float64(zone.Connections))
		metrics["discarded"].With(zoneLabels).Set(float64(zone.Discarded))

		metrics["sessions_1xx"].With(zoneLabels).Set(float64(zone.Sessions.Sessions1xx))
		metrics["sessions_2xx"].With(zoneLabels).Set(float64(zone.Sessions.Sessions2xx))
		metrics["sessions_4xx"].With(zoneLabels).Set(float64(zone.Sessions.Sessions4xx))
		metrics["sessions_5xx"].With(zoneLabels).Set(float64(zone.Sessions.Sessions5xx))
		metrics["sessions_total"].With(zoneLabels).Set(float64(zone.Sessions.Total))
	}
}

func scrapeSsl(ssl nginx.Ssl, metrics map[string]*prometheus.GaugeVec) {
	metrics["ssl_handshakes"].With(nil).Set(float64(ssl.Handshakes))
	metrics["ssl_handshakes_failed"].With(nil).Set(float64(ssl.HandshakesFailed))
	metrics["ssl_session_reuses"].With(nil).Set(float64(ssl.SessionReuses))
}

func scrapeRequests(requests nginx.Requests, metrics map[string]*prometheus.GaugeVec) {
	metrics["requests_total"].With(nil).Set(float64(requests.Total))
	metrics["requests"].With(nil).Set(float64(requests.Current))
}

func scrapeConnections(connections nginx.Connections, metrics map[string]*prometheus.GaugeVec) {
	metrics["connections_accepted"].With(nil).Set(float64(connections.Accepted))
	metrics["connections_dropped"].With(nil).Set(float64(connections.Dropped))
	metrics["connections_active"].With(nil).Set(float64(connections.Active))
	metrics["connections_idle"].With(nil).Set(float64(connections.Idle))
}

func (e *Exporter) resetMetrics() {
	for _, m := range e.httpServerZonesMetrics {
		m.Reset()
	}
	for _, m := range e.httpUpstreamMetrics {
		m.Reset()
	}
	for _, m := range e.streamServerZonesMetrics {
		m.Reset()
	}
	for _, m := range e.streamUpstreamMetrics {
		m.Reset()
	}
	for _, m := range e.serverMetrics {
		m.Reset()
	}
}

func (e *Exporter) collectMetrics(metrics chan<- prometheus.Metric) {
	for _, m := range e.httpServerZonesMetrics {
		m.Collect(metrics)
	}
	for _, m := range e.httpUpstreamMetrics {
		m.Collect(metrics)
	}
	for _, m := range e.streamServerZonesMetrics {
		m.Collect(metrics)
	}
	for _, m := range e.streamUpstreamMetrics {
		m.Collect(metrics)
	}
	for _, m := range e.serverMetrics {
		m.Collect(metrics)
	}
}

func newServerMetric(namespace string, metricName string, docString string, constLabels prometheus.Labels) *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace:   namespace,
			Name:        "server_" + metricName,
			Help:        docString,
			ConstLabels: constLabels,
		},
		nil,
	)
}

func newHttpServerZoneMetric(namespace string, metricName string, docString string, constLabels prometheus.Labels) *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace:   namespace,
			Name:        "http_server_zone_" + metricName,
			Help:        docString,
			ConstLabels: constLabels,
		},
		serverZoneLabelNames,
	)
}

func newHttpUpstreamMetric(namespace string, metricName string, docString string, constLabels prometheus.Labels) *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace:   namespace,
			Name:        "http_upstream_peer_" + metricName,
			Help:        docString,
			ConstLabels: constLabels,
		},
		upstreamLabelNames,
	)
}

func newStreamServerZoneMetric(namespace string, metricName string, docString string, constLabels prometheus.Labels) *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace:   namespace,
			Name:        "stream_server_zone_" + metricName,
			Help:        docString,
			ConstLabels: constLabels,
		},
		serverZoneLabelNames,
	)
}

func newStreamUpstreamMetric(namespace string, metricName string, docString string, constLabels prometheus.Labels) *prometheus.GaugeVec {
	return prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace:   namespace,
			Name:        "stream_upstream_peer_" + metricName,
			Help:        docString,
			ConstLabels: constLabels,
		},
		upstreamLabelNames,
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

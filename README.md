# DEPRECATED
This project is not longer maintained. Please use the official NGINX Plus Exporter:

https://github.com/nginxinc/nginx-prometheus-exporter

# nginxplus_exporter

This is a simple server that scrapes NGINX Plus stats and exports them via HTTP for Prometheus consumption.
See http://nginx.org/en/docs/http/ngx_http_api_module.html. This exporter is compatible with version 3 of the API.

Inspired by [haproxy_exporter](https://github.com/prometheus/haproxy_exporter) from the prometheus maintainers.

## Getting Started
To run it:

```bash
./nginxplus_exporter [flags]
```

Help on flags:

```bash
./nginxplus_exporter --help
```

For more information check the source code documentation.

## Usage

### HTTP NGINX Plus API URL

Specify custom URLs for the NGINX Plus API uri using the --nginx.scrape-uri flag. For example, if you have set stats uri /api,

```bash
nginxplus_exporter --nginx.scrape-uri="http://localhost:1080/api"
```

If your stats port is protected by basic auth, add the credentials to the scrape URL:

```bash
nginxplus_exporter --nginx.scrape-uri="http://user:pass@localhost:1080/api"
```

You can also scrape HTTPS URLs. Certificate validation is enabled by default, but you can disable it using the `--nginx.ssl-verify=false` flag:

```bash
nginxplus_exporter --nginx.scrape-uri="https://nginx.example.com/api" --nginx.ssl-verify=false
```

### Docker

[![Docker Pulls](https://img.shields.io/docker/pulls/avthart/nginxplus-exporter.svg?maxAge=604800)](https://hub.docker.com/r/avthart/nginxplus-exporter/)

```bash
docker run -p 9102:9102 avthart/nginxplus-exporter:v0.1.0 --nginx.scrape-uri="http://localhost:1080/api" 
```

See https://hub.docker.com/r/avthart/nginxplus-exporter/

## Development

### Building

```bash
make build
```

### Testing

[![CircleCI](https://circleci.com/gh/avthart/nginxplus_exporter/tree/master.svg?style=shield)](https://circleci.com/gh/avthart/nginxplus_exporter)

```bash
make test
```

## License

Apache License 2.0, see [LICENSE](LICENSE.md).

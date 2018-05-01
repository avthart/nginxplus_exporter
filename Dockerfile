FROM        quay.io/prometheus/busybox:latest
MAINTAINER  Albert van t Hart <avthart@gmail.com>

COPY nginxplus_exporter /bin/nginxplus_exporter

ENTRYPOINT ["/bin/nginxplus_exporter"]
EXPOSE     9102
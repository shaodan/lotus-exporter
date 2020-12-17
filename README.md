# lotus-exporter

Prometheus Exporter for lotus 
https://prometheus.io/docs/instrumenting/writing_exporters/

# How to Run

## Install Golang

wget https://dl.google.com/go/go1.15.2.linux-amd64.tar.gz
sudo tar -xvf go1.15.3.linux-amd64.tar.gz
sudo mv go /usr/local  

## Complier

export GO111MODULE=on
export GOPROXY=https://goproxy.io

go build .

## Run

./lotus-exporter

## Use

curl http://localhost:9002/metrics

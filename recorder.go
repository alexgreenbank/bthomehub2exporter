package main

import (
	"fmt"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	prefix = "" // TODO
)

type Recorder interface {
	measureConnected(up int)
	measurePolls()
	measureStatusCode(code int)
	measureLastGoodPoll(ts time.Time)
	measureLastPoll(ts time.Time) // Time (UTC) of last poll
	measurePollDur(duration time.Duration)
	measureParseDur(duration time.Duration)
	measureLastError(ts time.Time) // Time (UTC) of last errored poll
	measureSpeedUp(speed int)
	measureSpeedDown(speed int)
	measureBytesUp(bytes int)
	measureBytesDown(bytes int)
	measureSysUptime(ts int)
	measureConnUptime(ts int)
}

func NewRecorder(reg prometheus.Registerer) Recorder {
	r := &prometheusRecorder{
		btsmarthub2Polls: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: prefix,
			Name:      "btsmarthub2Polls",
			Help:      "Number of polls we have attempted",
		}),
		btsmarthub2Connected: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: prefix,
			Name:      "btsmarthub2Connected",
			Help:      "Whether the Broadband is connected",
		}),
		btsmarthub2StatusCodeCount: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: prefix,
			Name:      "btsmarthub2StatusCodeCount",
			Help:      "A count of each status code encountered",
		}, []string{"statusCode"}),
		btsmarthub2LastGoodPollSeconds: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: prefix,
			Name:      "btsmarthub2LastGoodPollSeconds",
			Help:      "The UNIX timestamp in seconds of the last good poll",
		}),
		btsmarthub2LastPollSeconds: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: prefix,
			Name:      "btsmarthub2LastPollSeconds",
			Help:      "The UNIX timestamp in seconds of the last poll",
		}),
		btsmarthub2PollDurSeconds: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: prefix,
			Name:      "btsmarthub2PollDurSeconds",
			Help:      "The total duration of polling",
		}),
		btsmarthub2ParseDurSeconds: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: prefix,
			Name:      "btsmarthub2ParseDurSeconds",
			Help:      "The total duration of parsing",
		}),
		btsmarthub2LastErrorTimeSeconds: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: prefix,
			Name:      "btsmarthub2LastErrorTimeSeconds",
			Help:      "The UNIX timestamp in seconds of the last error",
		}),
		btsmarthub2SpeedUp: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: prefix,
			Name:      "btsmarthub2SpeedUp",
			Help:      "The upload broadband speed in bps",
		}),
		btsmarthub2SpeedDown: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: prefix,
			Name:      "btsmarthub2SpeedDown",
			Help:      "The download broadband speed in bps",
		}),
		btsmarthub2BytesUp: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: prefix,
			Name:      "btsmarthub2BytesUp",
			Help:      "The number of bytes uploaded",
		}),
		btsmarthub2BytesDown: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: prefix,
			Name:      "btsmarthub2BytesDown",
			Help:      "The number of bytes downloaded",
		}),
		btsmarthub2SysUptimeSeconds: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: prefix,
			Name:      "btsmarthub2SysUptimeSeconds",
			Help:      "The sysuptime of the BB router in seconds",
		}),
		btsmarthub2ConnUptimeSeconds: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: prefix,
			Name:      "btsmarthub2ConnUptimeSeconds",
			Help:      "The connuptime of the BB router in seconds",
		}),
	}

	reg.MustRegister(r.btsmarthub2Polls, r.btsmarthub2Connected, r.btsmarthub2StatusCodeCount, r.btsmarthub2LastGoodPollSeconds,
		r.btsmarthub2LastPollSeconds, r.btsmarthub2PollDurSeconds, r.btsmarthub2ParseDurSeconds,
		r.btsmarthub2LastErrorTimeSeconds, r.btsmarthub2SpeedUp, r.btsmarthub2SpeedDown, r.btsmarthub2BytesUp,
		r.btsmarthub2BytesDown, r.btsmarthub2SysUptimeSeconds, r.btsmarthub2ConnUptimeSeconds)
	return r
}

type prometheusRecorder struct {
	btsmarthub2Polls                prometheus.Counter
	btsmarthub2Connected            prometheus.Gauge
	btsmarthub2StatusCodeCount      *prometheus.CounterVec
	btsmarthub2LastGoodPollSeconds  prometheus.Gauge
	btsmarthub2LastPollSeconds      prometheus.Gauge
	btsmarthub2PollDurSeconds       prometheus.Counter
	btsmarthub2ParseDurSeconds      prometheus.Counter
	btsmarthub2LastErrorTimeSeconds prometheus.Gauge
	btsmarthub2SpeedUp              prometheus.Gauge
	btsmarthub2SpeedDown            prometheus.Gauge
	btsmarthub2BytesUp              prometheus.Gauge
	btsmarthub2BytesDown            prometheus.Gauge
	btsmarthub2SysUptimeSeconds     prometheus.Gauge
	btsmarthub2ConnUptimeSeconds    prometheus.Gauge
}

// Count the number of polls we have made
func (r prometheusRecorder) measurePolls() {
	r.btsmarthub2Polls.Inc()
}

// Whether the Broadband is connected
func (r prometheusRecorder) measureConnected(up int) {
	r.btsmarthub2Connected.Set(float64(up))
}

// A count of each status code encountered
func (r prometheusRecorder) measureStatusCode(code int) {
	codeStr := fmt.Sprintf("%d", code)
	r.btsmarthub2StatusCodeCount.WithLabelValues(codeStr).Add(1)
}

// The UNIX timestamp in seconds of the last good poll
func (r prometheusRecorder) measureLastGoodPoll(ts time.Time) {
	r.btsmarthub2LastGoodPollSeconds.Set(float64(ts.Unix()))
}

// The UNIX timestamp in seconds of the last poll
func (r prometheusRecorder) measureLastPoll(ts time.Time) {
	r.btsmarthub2LastPollSeconds.Set(float64(ts.Unix()))
}

// The duration of the poll
func (r prometheusRecorder) measurePollDur(duration time.Duration) {
	r.btsmarthub2PollDurSeconds.Add(duration.Seconds())
}

// The duration of the parse
func (r prometheusRecorder) measureParseDur(duration time.Duration) {
	r.btsmarthub2ParseDurSeconds.Add(duration.Seconds())
}

// The UNIX timestamp in seconds of the last error
func (r prometheusRecorder) measureLastError(ts time.Time) {
	r.btsmarthub2LastErrorTimeSeconds.Set(float64(ts.Unix()))
}

// The upload broadband speed in bps
func (r prometheusRecorder) measureSpeedUp(speed int) {
	r.btsmarthub2SpeedUp.Set(float64(speed))
}

// The download broadband speed in bps
func (r prometheusRecorder) measureSpeedDown(speed int) {
	r.btsmarthub2SpeedDown.Set(float64(speed))
}

// The number of bytes uploaded
func (r prometheusRecorder) measureBytesUp(bytes int) {
	r.btsmarthub2BytesUp.Set(float64(bytes))
}

// The number of bytes downloaded
func (r prometheusRecorder) measureBytesDown(bytes int) {
	r.btsmarthub2BytesDown.Set(float64(bytes))
}

// The sysuptime of the BB router in seconds
func (r prometheusRecorder) measureSysUptime(ts int) {
	r.btsmarthub2SysUptimeSeconds.Set(float64(ts))
}

// The connuptime of the BB router in seconds
func (r prometheusRecorder) measureConnUptime(ts int) {
	r.btsmarthub2ConnUptimeSeconds.Set(float64(ts))
}

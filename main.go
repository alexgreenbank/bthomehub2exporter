package main

// I'm a long standing C developer learning Go
// Any comments on more idiomatic Go are welcome

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antchfx/xmlquery"
	// "github.com/antchfx/xpath"
)

const (
	// States
	S_INIT = iota
	S_CONNECTED = iota
	S_DISCONNECTED = iota
	S_TIMEOUT = iota
	S_ERROR = iota
	S_PARSEFAIL = iota
	usec_in_s = 1000000.0
)


var (
	mu		sync.Mutex
	hc		*http.Client
	state = S_INIT
	lastgoodpoll	time.Time // timestamp of last successful poll
	lastpoll	time.Time // timestamp of last poll
	laststatuscode	int // http response code of last poll
	lastpolldur	time.Duration // elapsed time for last poll
	lasterror	string // string of error from http client or parser, blank if none
	lasterrortime	time.Time // time of last error
	bps_up		int // speed up/down
	bps_down	int
	bbu_up		int // bb usage up/down
	bbu_down	int
	sysuptime	int
	cfg		Config
)

type Config struct {
	timeout	 time.Duration // timeout in msec
	ipaddress	string // IP address
	port		int // port
	metricport	int // metricport
	pollinterval	time.Duration // polling interval
	pollurl	 string // url for polling
	metricurl	string // url for metrics
	datastore	string // datastore ("" means no storage)
}

func RegisterFlagsWithPrefix(prefix string) {
	flag.DurationVar(&cfg.timeout, prefix+"timeout", 1*time.Second, "Timeout for polling router")
	flag.StringVar(&cfg.ipaddress, prefix+"ipaddress", "192.168.1.254", "IP Address of router")
	flag.IntVar(&cfg.port, prefix+"port", 80, "Port of router")
	flag.IntVar(&cfg.metricport, prefix+"metricport", 9090, "port for serving metrics")
	flag.DurationVar(&cfg.pollinterval, prefix+"interval", 10*time.Second, "Polling interval")
	flag.StringVar(&cfg.pollurl, prefix+"pollurl", "nonAuth/wan_conn.xml", "URL to poll")
	flag.StringVar(&cfg.metricurl, prefix+"metricurl", "/metrics", "URL to server metrics")
	flag.StringVar(&cfg.datastore, prefix+"datastore", "", "Datastore directory, blank to disable")
}

func parseXML(body string, polltime time.Time, dur_poll time.Duration, statuscode int) error {
	speed_up := 0
	speed_down := 0
	bytes_up := 0
	bytes_down := 0
	uptime := 0

	doc, err := xmlquery.Parse(strings.NewReader(body))
	if err != nil {
		log.Fatal(err)
	}

	mu.Lock()
	state = S_CONNECTED
	lastgoodpoll = polltime
	lastpoll = polltime
	laststatuscode = statuscode
	lastpolldur = dur_poll
	bps_up = speed_up
	bps_down = speed_down
	bbu_up = bytes_up
	bbu_down = bytes_down
	sysuptime = uptime
	mu.Unlock()

	return nil
}

func HandleMetrics(w http.ResponseWriter, req *http.Request) {
	// Grab a string representation of all of the metrics
	mu.Lock()
	curr_state := state
	str_lastgoodpoll := fmt.Sprintf("%.6f", float64(lastgoodpoll.UnixMicro())/usec_in_s)
	str_lastpoll := fmt.Sprintf("%.6f", float64(lastpoll.UnixMicro())/usec_in_s)
	str_laststatuscode := strconv.Itoa(laststatuscode)
	str_lastpolldur := fmt.Sprintf("%.6f", float64(lastpolldur.Microseconds())/usec_in_s)
	str_lasterror := lasterror
	str_lasterrortime := fmt.Sprintf("%.6f", float64(lasterrortime.UnixMicro())/usec_in_s)
	str_bps_up := strconv.Itoa(bps_up)
	str_bps_down := strconv.Itoa(bps_down)
	str_bbu_up := strconv.Itoa(bbu_up)
	str_bbu_down := strconv.Itoa(bbu_down)
	str_sysuptime := strconv.Itoa(sysuptime)
	mu.Unlock()
	// Create the response
	response_text := ""
	if curr_state == S_INIT {
		response_text = "Argle\n"
	} else if curr_state == S_CONNECTED {
		response_text += "# HELP btsmarthub2_connected Whether the Broadband is connected\n"
		response_text += "# TYPE btsmarthub2_connected gauge\n"
		response_text += "btsmarthub2_connected 1\n"

		response_text += "# HELP btsmarthub2_laststatuscode The HTTP status code of the last poll\n"
		response_text += "# TYPE btsmarthub2_laststatuscode gauge\n"
		response_text += "btsmarthub2_laststatuscode "+str_laststatuscode+"\n"

		response_text += "# HELP btsmarthub2_lastgoodpoll_seconds The UNIX timestamp in seconds of the last good poll\n"
		response_text += "# TYPE btsmarthub2_lastgoodpoll_seconds counter\n"
		response_text += "btsmarthub2_lastgoodpoll_seconds "+str_lastgoodpoll+"\n"

		response_text += "# HELP btsmarthub2_lastpoll_seconds The UNIX timestamp in seconds of the last poll\n"
		response_text += "# TYPE btsmarthub2_lastpoll_seconds counter\n"
		response_text += "btsmarthub2_lastpoll_seconds "+str_lastpoll+"\n"

		response_text += "# HELP btsmarthub2_lastpolldur_seconds The duration of the poll\n"
		response_text += "# TYPE btsmarthub2_lastpolldur_seconds counter\n"
		response_text += "btsmarthub2_lastpolldur_seconds "+str_lastpolldur+"\n"

		if str_lasterror != "" {
			response_text += "# HELP btsmarthub2_lasterrortime_seconds The UNIX timestamp in seconds of the last error\n"
			response_text += "# TYPE btsmarthub2_lasterrortime_seconds counter\n"
			response_text += "btsmarthub2_lasterrortime_seconds "+str_lasterrortime+"\n"
		}

		response_text += "# HELP btsmarthub2_bb_speed_up The upload broadband speed in bps\n"
		response_text += "# TYPE btsmarthub2_bb_speed_up gauge\n"
		response_text += "btsmarthub2_bb_speed_up "+str_bps_up+"\n"

		response_text += "# HELP btsmarthub2_bb_speed_down The download broadband speed in bps\n"
		response_text += "# TYPE btsmarthub2_bb_speed_down gauge\n"
		response_text += "btsmarthub2_bb_speed_down "+str_bps_down+"\n"

		response_text += "# HELP btsmarthub2_bb_bytes_up The number of bytes uploaded\n"
		response_text += "# TYPE btsmarthub2_bb_bytes_up counter\n"
		response_text += "btsmarthub2_bb_bytes_up "+str_bbu_up+"\n"

		response_text += "# HELP btsmarthub2_bb_bytes_down The number of bytes downloaded\n"
		response_text += "# TYPE btsmarthub2_bb_bytes_down counter\n"
		response_text += "btsmarthub2_bb_bytes_down "+str_bbu_down+"\n"

		response_text += "# HELP btsmarthub2_sysuptime The sysuptime of the BB router in seconds\n"
		response_text += "# TYPE btsmarthub2_sysuptime counter\n"
		response_text += "btsmarthub2_sysuptime "+str_sysuptime+"\n"
	} else {
		response_text = "unknown_state:"+strconv.Itoa(curr_state)+"\n"
		// TODO
	}
	// TODO - handle other states
	// Send the response
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-type:", "text/plain")
	w.Write([]byte(response_text))
}

func doPoll() {
	url := fmt.Sprintf("http://%s:%d/%s", cfg.ipaddress, cfg.port, cfg.pollurl)
	polltime := time.Now()
	resp, err := hc.Get(url)
	dur_poll := time.Since(polltime)

	if err != nil {
		fmt.Printf("Got error [%s]\n", err)
		mu.Lock()
		state = S_ERROR
		lastpoll = polltime
		lastpolldur = dur_poll
		lasterror = err.Error()
		lasterrortime = polltime
		mu.Unlock()
		return
	}
	// Got a good response!
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		// TODO
		return
	}

	// Stash the downloaded file if we have a datastore configured
	if cfg.datastore != "" {
		fname := cfg.datastore+"/"+polltime.Format("20060102150405.000000")
		fmt.Printf("fname = [%s]\n", fname)
		os.WriteFile(fname, body, 0644)
	}
	// TODO - parse file and update values
	err = parseXML(string(body), polltime, dur_poll, resp.StatusCode)
	if err != nil {
		// TODO Do something
	}

}

func main() {
	RegisterFlagsWithPrefix("")
	flag.Parse()

	http.HandleFunc(cfg.metricurl, HandleMetrics)
	listenaddr := ":"+strconv.Itoa(cfg.metricport)
	go func() {
		err := http.ListenAndServe(listenaddr, nil)
		if err != nil {
			log.Fatal("ListenAndServe:", err)
		}
	}()

	hc = &http.Client{
		Timeout: cfg.timeout,
	}
	for {
		doPoll()
		time.Sleep(cfg.pollinterval)
	}
}

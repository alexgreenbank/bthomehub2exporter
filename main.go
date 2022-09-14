package main

// I'm a long standing C developer learning Go
// Any comments on more idiomatic Go are welcome

// TODO - library-ify the code to make it easier to include it elsewhere
// TODO - simplify with move to promtool

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antchfx/xmlquery"
)

const (
	// States
	S_INIT         = iota
	S_CONNECTED    = iota
	S_DISCONNECTED = iota
	S_TIMEOUT      = iota
	S_ERROR        = iota
	S_PARSEFAIL    = iota
	usec_in_s      = 1000000.0
)

var (
	mu                           sync.Mutex
	hc                           *http.Client
	state                        = S_INIT
	lastgoodpoll                 time.Time     // timestamp of last successful poll
	lastpoll                     time.Time     // timestamp of last poll
	laststatuscode               int           // http response code of last poll
	lastpolldur                  time.Duration // elapsed time for last poll
	lastparsedur                 time.Duration // elapsed time for last parsing
	lasterror                    string        // string of error from http client or parser, blank if none
	lasterrortime                time.Time     // time of last error
	bps_up                       int           // speed up/down
	bps_down                     int
	bbu_up                       int // bb usage up/down
	bbu_down                     int
	sysuptime                    int
	connuptime                   int
	cfg                          Config
	substring_square_brackets_re = regexp.MustCompile(`([^[\]]+)`)
)

type Config struct {
	timeout      time.Duration // timeout in msec
	ipaddress    string        // IP address
	port         int           // port
	metricport   int           // metricport
	pollinterval time.Duration // polling interval
	pollurl      string        // url for polling
	metricurl    string        // url for metrics
	datastore    string        // datastore ("" means no storage)
	file         string        // filename to parse straight away
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
	flag.StringVar(&cfg.file, prefix+"file", "", "Parse file directly")
}

func parseXML(body string, polltime time.Time, dur_poll time.Duration, statuscode int) error {
	speed_up := 0
	speed_down := 0
	bytes_up := 0
	bytes_down := 0
	s_uptime := 0
	c_uptime := 0

	parsetime := time.Now()
	doc, err := xmlquery.Parse(strings.NewReader(body))
	dur_parse := time.Since(parsetime)
	if err != nil {
		mu.Lock()
		state = S_PARSEFAIL
		lastpoll = polltime
		laststatuscode = statuscode
		lastpolldur = dur_poll
		lastparsedur = dur_parse
		lasterror = err.Error()
		lasterrortime = parsetime
		mu.Unlock()
		log.Fatal("parsing:", err)
		return err
	}
	// fmt.Printf("parsing\n")
	// Parsed ok, find top level status
	x_status := xmlquery.FindOne(doc, "//status")
	if x_status == nil {
		log.Fatal("No <status>")
		// TODO
		return nil
	}
	// Find <link_status>
	if val := xmlquery.FindOne(x_status, "//link_status/@value"); val != nil {
		// fmt.Printf("Got raw link_status value of [%s]\n", val.InnerText())
		str, err := url.QueryUnescape(val.InnerText())
		if err != nil {
			// TODO handle this better
			log.Fatal("QueryUnescape:link_status:", err)
		}
		str = strings.Replace(str, "\n", "", -1)
		// fmt.Printf("Got link_status value of [%s]\n", str)
		if strings.HasPrefix(str, "connected;") {
			// <link_status value="connected%3Bvdsl%3B462385"/>
			n, err := fmt.Sscanf(str, "connected;vdsl;%d", &c_uptime)
			if err != nil {
				// TODO - failed to convert uptime
				log.Fatal("Failed to convert conn_uptime:1:", err)
			}
			if n != 1 {
				// TODO - failed to convert uptime
				log.Fatal("Failed to convert conn_uptime:2:", err)
			}
			// fmt.Printf("Converted conn_uptime of %d\n", c_uptime)
		} else if strings.HasPrefix(str, "disconnected;") {
			// <link_status value="disconnected%3Badsl%3B0" />
			mu.Lock()
			state = S_DISCONNECTED
			connuptime = 0
			lastpoll = polltime
			laststatuscode = statuscode
			lastpolldur = dur_poll
			lastparsedur = dur_parse
			if err != nil {
				lasterror = err.Error()
			}
			lasterrortime = parsetime
			mu.Unlock()
			return nil
		} else { // TODO handle more states
			// TODO - handle this more gracefully
			log.Print("Unexpected link_status value:", val)
		}
	} else {
		// TODO - can't get link_status
		log.Fatal("No <link_status>")
	}

	// sysuptime
	// <sysuptime value="973412"/>
	if val := xmlquery.FindOne(x_status, "//sysuptime/@value"); val != nil {
		// fmt.Printf("Got sysuptime value of [%s]\n", val.InnerText())
		s_uptime, err = strconv.Atoi(val.InnerText())
		if err != nil {
			// TODO - failed to convert uptime
			log.Fatal("Failed to convert uptime:", err)
		}
		// fmt.Printf("Converted sysuptime of %d\n", s_uptime)
	} else {
		// TODO - can't get sysuptime
		log.Fatal("No <sysuptime>")
	}

	// speeds from status_rate
	// <status_rate type="array" value="[['0%3B0%3B0%3B0'], ['18567000%3B65059000%3B0%3B0'], ['0%3B0%3B0%3B0'], null]"/>
	if val := xmlquery.FindOne(x_status, "//status_rate/@value"); val != nil {
		// fmt.Printf("Got raw status_rate value of [%s]\n", val.InnerText())
		str, err := url.QueryUnescape(val.InnerText())
		if err != nil {
			// TODO handle this better
			log.Fatal("QueryUnescape:status_rate:", err)
		}
		str = strings.Replace(str, "\n", "", -1)
		// fmt.Printf("Got status_rate value of [%s]\n", str)
		segs := substring_square_brackets_re.FindAllString(str, -1)
		found := false
		for _, z := range segs {
			if z != "'0;0;0;0'" && z != "," && z != ",null" {
				n, err := fmt.Sscanf(z, "'%d;%d;0;0'", &speed_up, &speed_down)
				if err != nil {
					log.Fatal("status_rate:format1")
				}
				if n != 2 {
					log.Fatal("status_rate:format2")
				}
				if found {
					log.Fatal("status_rate:Already found")
				}
				found = true
				// fmt.Printf("Got %d and %d for status_rate\n", speed_up, speed_down)
			}
		}
	} else {
		// TODO - can't get status_rate
		log.Fatal("No <status_rate>")
	}

	// bytes up/down
	// <wan_conn_volume_list type="array" value="[['0%3B0%3B0'], ['81650478525%3B67192513981%3B14457964544'], ['0%3B0%3B0'], null]"/>
	if val := xmlquery.FindOne(x_status, "//wan_conn_volume_list/@value"); val != nil {
		// fmt.Printf("Got raw wan_conn_volume_list value of [%s]\n", val.InnerText())
		str, err := url.QueryUnescape(val.InnerText())
		if err != nil {
			// TODO handle this better
			log.Fatal("QueryUnescape:wan_conn_volume_list:", err)
		}
		str = strings.Replace(str, "\n", "", -1)
		// fmt.Printf("Got wan_conn_volume_list value of [%s]\n", str)
		segs := substring_square_brackets_re.FindAllString(str, -1)
		found := false
		for _, z := range segs {
			if z != "'0;0;0'" && z != "," && z != ",null" {
				bytes_total := 0
				n, err := fmt.Sscanf(z, "'%d;%d;%d'", &bytes_total, &bytes_down, &bytes_up)
				if err != nil {
					log.Fatal("wan_conn_volume_list:format1", z)
				}
				if n != 3 {
					log.Fatal("wan_conn_volume_list:format2")
				}
				if found {
					log.Fatal("wan_conn_volume_list:Already found")
				}
				found = true
				// fmt.Printf("Got %d and %d for wan_conn_volume_list\n", bytes_up, bytes_down)
			}
		}
	} else {
		// TODO - can't get wan_conn_volume_list
		log.Fatal("No <wan_conn_volume_list>")
	}

	// TODO anything else from this?
	// <wan_linestatus_rate_list type="array" value="[['UP','VDSL2','Profile%2017a','31','58','186','83','202','83','130','74','65059','18567','65682000','18564000','fast'], null]"/>

	mu.Lock()
	state = S_CONNECTED
	lastgoodpoll = polltime
	lastpoll = polltime
	laststatuscode = statuscode
	lastpolldur = dur_poll
	lastparsedur = dur_parse
	bps_up = speed_up
	bps_down = speed_down
	bbu_up = bytes_up
	bbu_down = bytes_down
	sysuptime = s_uptime
	connuptime = c_uptime
	mu.Unlock()

	return nil
}

func metricText() string {
	// Build the response text based on current state
	mu.Lock()
	curr_state := state
	str_lastgoodpoll := fmt.Sprintf("%.6f", float64(lastgoodpoll.UnixMicro())/usec_in_s)
	str_lastpoll := fmt.Sprintf("%.6f", float64(lastpoll.UnixMicro())/usec_in_s)
	str_laststatuscode := strconv.Itoa(laststatuscode)
	str_lastpolldur := fmt.Sprintf("%.6f", float64(lastpolldur.Microseconds())/usec_in_s)
	str_lastparsedur := fmt.Sprintf("%.6f", float64(lastparsedur.Microseconds())/usec_in_s)
	str_lasterror := lasterror
	str_lasterrortime := fmt.Sprintf("%.6f", float64(lasterrortime.UnixMicro())/usec_in_s)
	str_bps_up := strconv.Itoa(bps_up)
	str_bps_down := strconv.Itoa(bps_down)
	str_bbu_up := strconv.Itoa(bbu_up)
	str_bbu_down := strconv.Itoa(bbu_down)
	str_sysuptime := strconv.Itoa(sysuptime)
	str_connuptime := strconv.Itoa(connuptime)
	mu.Unlock()

	// Create the response
	response_text := ""
	if curr_state == S_INIT {
		response_text = "Argle\n"
	} else if curr_state == S_DISCONNECTED {
		response_text += "# HELP btsmarthub2_connected Whether the Broadband is connected\n"
		response_text += "# TYPE btsmarthub2_connected gauge\n"
		response_text += "btsmarthub2_connected 0\n"

		response_text += "# HELP btsmarthub2_laststatuscode The HTTP status code of the last poll\n"
		response_text += "# TYPE btsmarthub2_laststatuscode gauge\n"
		response_text += "btsmarthub2_laststatuscode " + str_laststatuscode + "\n"

		response_text += "# HELP btsmarthub2_lastgoodpoll_seconds The UNIX timestamp in seconds of the last good poll\n"
		response_text += "# TYPE btsmarthub2_lastgoodpoll_seconds counter\n"
		response_text += "btsmarthub2_lastgoodpoll_seconds " + str_lastgoodpoll + "\n"

		response_text += "# HELP btsmarthub2_lastpoll_seconds The UNIX timestamp in seconds of the last poll\n"
		response_text += "# TYPE btsmarthub2_lastpoll_seconds counter\n"
		response_text += "btsmarthub2_lastpoll_seconds " + str_lastpoll + "\n"

		response_text += "# HELP btsmarthub2_lastpolldur_seconds The duration of the poll\n"
		response_text += "# TYPE btsmarthub2_lastpolldur_seconds counter\n"
		response_text += "btsmarthub2_lastpolldur_seconds " + str_lastpolldur + "\n"

		response_text += "# HELP btsmarthub2_lastparsedur_seconds The duration of the poll\n"
		response_text += "# TYPE btsmarthub2_lastparsedur_seconds counter\n"
		response_text += "btsmarthub2_lastparsedur_seconds " + str_lastparsedur + "\n"

		if str_lasterror != "" {
			response_text += "# HELP btsmarthub2_lasterrortime_seconds The UNIX timestamp in seconds of the last error\n"
			response_text += "# TYPE btsmarthub2_lasterrortime_seconds counter\n"
			response_text += "btsmarthub2_lasterrortime_seconds " + str_lasterrortime + "\n"
		}

		response_text += "# HELP btsmarthub2_sysuptime_seconds The sysuptime of the BB router in seconds\n"
		response_text += "# TYPE btsmarthub2_sysuptime_seconds counter\n"
		response_text += "btsmarthub2_sysuptime_seconds " + str_sysuptime + "\n"

		response_text += "# HELP btsmarthub2_connuptime_seconds The connuptime of the BB connection in seconds\n"
		response_text += "# TYPE btsmarthub2_connuptime_seconds counter\n"
		response_text += "btsmarthub2_connuptime_seconds " + str_connuptime + "\n"

	} else if curr_state == S_CONNECTED {
		response_text += "# HELP btsmarthub2_connected Whether the Broadband is connected\n"
		response_text += "# TYPE btsmarthub2_connected gauge\n"
		response_text += "btsmarthub2_connected 1\n"

		response_text += "# HELP btsmarthub2_laststatuscode The HTTP status code of the last poll\n"
		response_text += "# TYPE btsmarthub2_laststatuscode gauge\n"
		response_text += "btsmarthub2_laststatuscode " + str_laststatuscode + "\n"

		response_text += "# HELP btsmarthub2_lastgoodpoll_seconds The UNIX timestamp in seconds of the last good poll\n"
		response_text += "# TYPE btsmarthub2_lastgoodpoll_seconds counter\n"
		response_text += "btsmarthub2_lastgoodpoll_seconds " + str_lastgoodpoll + "\n"

		response_text += "# HELP btsmarthub2_lastpoll_seconds The UNIX timestamp in seconds of the last poll\n"
		response_text += "# TYPE btsmarthub2_lastpoll_seconds counter\n"
		response_text += "btsmarthub2_lastpoll_seconds " + str_lastpoll + "\n"

		response_text += "# HELP btsmarthub2_lastpolldur_seconds The duration of the poll\n"
		response_text += "# TYPE btsmarthub2_lastpolldur_seconds counter\n"
		response_text += "btsmarthub2_lastpolldur_seconds " + str_lastpolldur + "\n"

		response_text += "# HELP btsmarthub2_lastparsedur_seconds The duration of the poll\n"
		response_text += "# TYPE btsmarthub2_lastparsedur_seconds counter\n"
		response_text += "btsmarthub2_lastparsedur_seconds " + str_lastparsedur + "\n"

		if str_lasterror != "" {
			response_text += "# HELP btsmarthub2_lasterrortime_seconds The UNIX timestamp in seconds of the last error\n"
			response_text += "# TYPE btsmarthub2_lasterrortime_seconds counter\n"
			response_text += "btsmarthub2_lasterrortime_seconds " + str_lasterrortime + "\n"
		}

		response_text += "# HELP btsmarthub2_bb_speed_up The upload broadband speed in bps\n"
		response_text += "# TYPE btsmarthub2_bb_speed_up gauge\n"
		response_text += "btsmarthub2_bb_speed_up " + str_bps_up + "\n"

		response_text += "# HELP btsmarthub2_bb_speed_down The download broadband speed in bps\n"
		response_text += "# TYPE btsmarthub2_bb_speed_down gauge\n"
		response_text += "btsmarthub2_bb_speed_down " + str_bps_down + "\n"

		response_text += "# HELP btsmarthub2_bb_bytes_up The number of bytes uploaded\n"
		response_text += "# TYPE btsmarthub2_bb_bytes_up counter\n"
		response_text += "btsmarthub2_bb_bytes_up " + str_bbu_up + "\n"

		response_text += "# HELP btsmarthub2_bb_bytes_down The number of bytes downloaded\n"
		response_text += "# TYPE btsmarthub2_bb_bytes_down counter\n"
		response_text += "btsmarthub2_bb_bytes_down " + str_bbu_down + "\n"

		response_text += "# HELP btsmarthub2_sysuptime_seconds The sysuptime of the BB router in seconds\n"
		response_text += "# TYPE btsmarthub2_sysuptime_seconds counter\n"
		response_text += "btsmarthub2_sysuptime_seconds " + str_sysuptime + "\n"

		response_text += "# HELP btsmarthub2_connuptime_seconds The connuptime of the BB connection in seconds\n"
		response_text += "# TYPE btsmarthub2_connuptime_seconds counter\n"
		response_text += "btsmarthub2_connuptime_seconds " + str_connuptime + "\n"
	} else {
		response_text = "unknown_state:" + strconv.Itoa(curr_state) + "\n"
		// TODO
	}
	// TODO - handle other states
	return response_text
}

func HandleMetrics(w http.ResponseWriter, req *http.Request) {
	output := metricText()
	// Send the response
	// Grab a string representation of all of the metrics
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-type:", "text/plain")
	w.Write([]byte(output))
}

func doPoll() {
	// url := fmt.Sprintf("http://%s:%d/%s", cfg.ipaddress, cfg.port, cfg.pollurl)
	url := fmt.Sprintf("http://%s/%s", cfg.ipaddress, cfg.pollurl)
	polltime := time.Now()
	req, err := http.NewRequest("GET", url, nil)
	req.Header.Set("Referer", "http://192.168.1.254/")
	resp, err := hc.Do(req)
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
		fname := cfg.datastore + "/" + polltime.Format("20060102150405.000000")
		// fmt.Printf("fname = [%s]\n", fname)
		os.WriteFile(fname, body, 0644)
	}
	// parse file and update values
	err = parseXML(string(body), polltime, dur_poll, resp.StatusCode)
	if err != nil {
		// TODO Do something
	}

}

func main() {
	RegisterFlagsWithPrefix("")
	flag.Parse()

	// check if we are parsing a single file
	if cfg.file != "" {
		// TODO read in file
		body, err := os.ReadFile(cfg.file)
		if err != nil {
			log.Fatal("ReadFile:", err)
		}
		// parse it
		err = parseXML(string(body), time.Now(), 1*time.Second, 0)
		if err != nil {
			log.Fatal("parseXML:", err)
		}
		// output data
		// output := metricText()
		// fmt.Printf("%s", output)
		return
	}

	http.HandleFunc(cfg.metricurl, HandleMetrics)
	listenaddr := ":" + strconv.Itoa(cfg.metricport)
	go func() {
		err := http.ListenAndServe(listenaddr, nil)
		if err != nil {
			log.Fatal("ListenAndServe:", err)
		}
	}()

	// Setup timeout on http client
	hc = &http.Client{
		Timeout: cfg.timeout,
	}

	// Infinite loop of polling
	for {
		doPoll()
		time.Sleep(cfg.pollinterval)
	}
}

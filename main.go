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
	"time"

	"github.com/antchfx/xmlquery"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	usec_in_s = 1000000.0
)

var (
	hc                        *http.Client
	cfg                       Config
	substringSquareBracketsRe = regexp.MustCompile(`([^[\]]+)`)
	recorder                  Recorder
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

func parseXML(body string) error {
	parseTime := time.Now()
	doc, err := xmlquery.Parse(strings.NewReader(body))
	durParse := time.Since(parseTime)

	recorder.measureParseDur(durParse)

	if err != nil {
		// TODO - do something with err.Error() ?
		recorder.measureLastError(parseTime)
		log.Fatal("parsing:", err)
		return err
	}
	// fmt.Printf("parsing\n")
	// Parsed ok, find top level status
	xStatus := xmlquery.FindOne(doc, "//status")
	if xStatus == nil {
		log.Fatal("No <status>")
		// TODO
		return nil
	}
	// Find <link_status>
	if val := xmlquery.FindOne(xStatus, "//link_status/@value"); val != nil {
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
			connUptime := 0
			n, err := fmt.Sscanf(str, "connected;vdsl;%d", &connUptime)
			if err != nil {
				// TODO - failed to convert uptime
				log.Fatal("Failed to convert conn_uptime:1:", err)
			}
			if n != 1 {
				// TODO - failed to convert uptime
				log.Fatal("Failed to convert conn_uptime:2:", err)
			}
			// fmt.Printf("Converted conn_uptime of %d\n", connUptime)
			recorder.measureConnected(1)
			recorder.measureConnUptime(connUptime)
		} else if strings.HasPrefix(str, "disconnected;") {
			// <link_status value="disconnected%3Badsl%3B0" />
			recorder.measureConnected(0)
			recorder.measureConnUptime(0)
			if err != nil {
				// TODO - do something with err.Error() ?
			}
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
	if val := xmlquery.FindOne(xStatus, "//sysuptime/@value"); val != nil {
		// fmt.Printf("Got sysuptime value of [%s]\n", val.InnerText())
		sysUptime, err := strconv.Atoi(val.InnerText())
		if err != nil {
			// TODO - failed to convert uptime
			log.Fatal("Failed to convert uptime:", err)
		}
		// fmt.Printf("Converted sysuptime of %d\n", sysUptime)
		recorder.measureSysUptime(sysUptime)
	} else {
		// TODO - can't get sysuptime
		log.Fatal("No <sysuptime>")
	}

	// speeds from status_rate
	// <status_rate type="array" value="[['0%3B0%3B0%3B0'], ['18567000%3B65059000%3B0%3B0'], ['0%3B0%3B0%3B0'], null]"/>
	if val := xmlquery.FindOne(xStatus, "//status_rate/@value"); val != nil {
		// fmt.Printf("Got raw status_rate value of [%s]\n", val.InnerText())
		str, err := url.QueryUnescape(val.InnerText())
		if err != nil {
			// TODO handle this better
			log.Fatal("QueryUnescape:status_rate:", err)
		}
		str = strings.Replace(str, "\n", "", -1)
		// fmt.Printf("Got status_rate value of [%s]\n", str)
		segs := substringSquareBracketsRe.FindAllString(str, -1)
		found := false
		for _, z := range segs {
			if z != "'0;0;0;0'" && z != "," && z != ",null" {
				speedUp := 0
				speedDown := 0
				n, err := fmt.Sscanf(z, "'%d;%d;0;0'", &speedUp, &speedDown)
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
				// fmt.Printf("Got %d and %d for status_rate\n", speedUp, speedDown)

				recorder.measureSpeedUp(speedUp)
				recorder.measureSpeedDown(speedDown)
			}
		}
	} else {
		// TODO - can't get status_rate
		log.Fatal("No <status_rate>")
	}

	// bytes up/down
	// <wan_conn_volume_list type="array" value="[['0%3B0%3B0'], ['81650478525%3B67192513981%3B14457964544'], ['0%3B0%3B0'], null]"/>
	if val := xmlquery.FindOne(xStatus, "//wan_conn_volume_list/@value"); val != nil {
		// fmt.Printf("Got raw wan_conn_volume_list value of [%s]\n", val.InnerText())
		str, err := url.QueryUnescape(val.InnerText())
		if err != nil {
			// TODO handle this better
			log.Fatal("QueryUnescape:wan_conn_volume_list:", err)
		}
		str = strings.Replace(str, "\n", "", -1)
		// fmt.Printf("Got wan_conn_volume_list value of [%s]\n", str)
		segs := substringSquareBracketsRe.FindAllString(str, -1)
		found := false
		for _, z := range segs {
			if z != "'0;0;0'" && z != "," && z != ",null" {
				bytesTotal := 0
				bytesUp := 0
				bytesDown := 0
				n, err := fmt.Sscanf(z, "'%d;%d;%d'", &bytesTotal, &bytesDown, &bytesUp)
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
				recorder.measureBytesUp(bytesUp)
				recorder.measureBytesDown(bytesDown)
				// fmt.Printf("Got %d and %d for wan_conn_volume_list\n", bytesUp, bytesDown)
			}
		}
	} else {
		// TODO - can't get wan_conn_volume_list
		log.Fatal("No <wan_conn_volume_list>")
	}

	// TODO anything else from this?
	// <wan_linestatus_rate_list type="array" value="[['UP','VDSL2','Profile%2017a','31','58','186','83','202','83','130','74','65059','18567','65682000','18564000','fast'], null]"/>

	return nil
}

func doPoll() {
	// url := fmt.Sprintf("http://%s:%d/%s", cfg.ipaddress, cfg.port, cfg.pollurl)
	url := fmt.Sprintf("http://%s/%s", cfg.ipaddress, cfg.pollurl)
	pollTime := time.Now()
	req, err := http.NewRequest("GET", url, nil)
	req.Header.Set("Referer", "http://192.168.1.254/")
	resp, err := hc.Do(req)
	durPoll := time.Since(pollTime)

	recorder.measureLastPoll(pollTime)
	recorder.measurePollDur(durPoll)

	if err != nil {
		fmt.Printf("Got error [%s]\n", err)
		// TODO - do anything with err.Error() ?
		recorder.measureLastError(pollTime)
		recorder.measurePolls("error")
		return
	}
	// Got a good response!
	defer resp.Body.Close()

	// Log the statusCode
	recorder.measureStatusCode(resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		// TODO
		recorder.measurePolls("error-readall")
		return
	}

	recorder.measurePolls("ok")
	recorder.measureLastGoodPoll(pollTime)

	// Stash the downloaded file if we have a datastore configured
	if cfg.datastore != "" {
		fname := cfg.datastore + "/" + pollTime.Format("20060102150405.000000")
		// fmt.Printf("fname = [%s]\n", fname)
		os.WriteFile(fname, body, 0644)
	}
	// parse file and update values
	err = parseXML(string(body))
	if err != nil {
		// TODO Do something
	}

}

func main() {
	RegisterFlagsWithPrefix("")
	flag.Parse()

	recorder = NewRecorder(prometheus.DefaultRegisterer) // TODO - prefix

	// check if we are parsing a single file
	if cfg.file != "" {
		// TODO read in file
		body, err := os.ReadFile(cfg.file)
		if err != nil {
			log.Fatal("ReadFile:", err)
		}
		// parse it
		err = parseXML(string(body))
		if err != nil {
			log.Fatal("parseXML:", err)
		}
		// output data
		// output := metricText()
		// fmt.Printf("%s", output)
		return
	}

	// Serve Prom metrics on cfg.metricport
	go func() {
		listenaddr := ":" + strconv.Itoa(cfg.metricport)
		http.Handle("/metrics", promhttp.Handler())
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

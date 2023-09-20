package task

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"
	"github.com/sirupsen/logrus"
)

const pushGateway = "https://pushgateway.stafi.io"

type monitorInfo struct {
	pusher    *push.Pusher
	heartbeat prometheus.Counter
	client    *http.Client
	instance  string
	job       string
	errInc    uint64
}

func (t *Task) initPusher(jobName string, account string) *monitorInfo {
	pushInfo := &monitorInfo{
		heartbeat: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "heartbeat",
		}),
		client:   &http.Client{},
		instance: account,
		job:      jobName,
		errInc:   0,
	}
	pushInfo.pusher = push.New(pushGateway, jobName).Collector(pushInfo.heartbeat)
	return pushInfo
}

func (t *Task) pushHeartbeat() {
	if !t.monitorEnable {
		return
	}
	if err := t.monitor.pusher.Grouping("instance", t.monitor.instance).
		Add(); err != nil {
		logrus.Errorf("Could not push heartbeat to PushGateway:%v", err)
	}
	t.monitor.heartbeat.Inc()
}

func (t *Task) pushErr(e error) {
	if !t.monitorEnable {
		return
	}

	defer func() {
		t.monitor.errInc += 1
	}()

	url := fmt.Sprintf("%s/metrics/job/%s/instance/%s/code/%d", pushGateway, t.monitor.job, t.monitor.instance, -1)
	method := "POST"

	errMsg := processIP(e.Error())

	payloadTemp := fmt.Sprintf(`# TYPE rtoken_rpc_error counter
	rtoken_rpc_error{msg="%s"} %d
	`, errMsg, t.monitor.errInc)

	payload := strings.NewReader(payloadTemp)

	req, err := http.NewRequest(method, url, payload)
	if err != nil {
		logrus.Errorln(err)
		return
	}
	req.Header.Add("Content-Type", "text/plain")

	res, err := t.monitor.client.Do(req)
	if err != nil {
		logrus.Errorln(err)
		return
	}
	defer res.Body.Close()

	_, err = io.ReadAll(res.Body)
	if err != nil {
		logrus.Errorln(err)
		return
	}
}

func processIP(str string) string {
	re := regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`)
	ips := re.FindAllString(str, -1)

	for _, ip := range ips {
		hiddenIP := hideIP(ip)
		str = strings.ReplaceAll(str, ip, hiddenIP)
	}

	return str
}

func hideIP(ip string) string {
	parts := strings.Split(ip, ".")
	if len(parts) == 4 {
		return fmt.Sprintf("%s.x.x.%s", parts[0], parts[3])
	}
	return ip
}

package task

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"
	"github.com/sirupsen/logrus"
)

type pushTaskInfo struct {
	pusher      *push.Pusher
	heartbeat   prometheus.Counter
	client      *http.Client
	instance    string
	pushGateway string
	job         string
	errInc      uint64
}

func (t *Task) initPusher(gateway string, jobName string, account string) *pushTaskInfo {
	if gateway == "" {
		return &pushTaskInfo{}
	}
	pushInfo := &pushTaskInfo{
		heartbeat: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "heartbeat",
		}),
		client:      &http.Client{},
		instance:    account,
		pushGateway: gateway,
		job:         jobName,
		errInc:      0,
	}
	pushInfo.pusher = push.New(pushInfo.pushGateway, jobName).Collector(pushInfo.heartbeat)
	return pushInfo
}

func (t *Task) pushHeartbeat() {
	if t.pushTask.pushGateway == "" {
		return
	}
	if err := t.pushTask.pusher.Grouping("instance", t.pushTask.instance).
		Add(); err != nil {
		logrus.Errorf("Could not push heartbeat to PushGateway:%v", err)
	}
	t.pushTask.heartbeat.Inc()
}

func (t *Task) pushErr(e error) {
	if t.pushTask.pushGateway == "" {
		return
	}

	defer func() {
		t.pushTask.errInc += 1
	}()

	url := fmt.Sprintf("%s/metrics/job/%s/instance/%s/code/%d", t.pushTask.pushGateway, t.pushTask.job, t.pushTask.instance, -1)
	method := "POST"

	payloadTemp := fmt.Sprintf(`# TYPE rtoken_rpc_error counter
	rtoken_rpc_error{msg="%s"} %d
	`, e.Error(), t.pushTask.errInc)

	payload := strings.NewReader(payloadTemp)

	req, err := http.NewRequest(method, url, payload)
	if err != nil {
		logrus.Errorln(err)
		return
	}
	req.Header.Add("Content-Type", "text/plain")

	res, err := t.pushTask.client.Do(req)
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

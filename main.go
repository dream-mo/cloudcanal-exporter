package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"
)

// 解析命令行参数
var addr = flag.String("listen-address", ":9119", "The address to listen on for HTTP requests.")
var url = flag.String("url", "http://localhost:8111", "CloudCanal base url")
var username = flag.String("username", "test@clougence.com", "Login username")
var password = flag.String("password", "clougence2021", "Login password")
var interval = flag.Int("interval", 30, "Clear cache interval(minute)")
var timeout = time.After(time.Second * time.Duration(*interval))
var jar, _ = cookiejar.New(nil)
var c = &http.Client{
	Timeout: time.Second * time.Duration(*interval),
	Jar:     jar,
}
var cacheEndTimestamp float64

// 获取许可证到期时间指标数据
func getLicenseExpireMetrics(base_url string, over chan float64) {
	url := base_url + "/cloudcanal/console/api/v1/inner/authcode/getauthedresourceinfo"
	res, e := c.Post(url, "application/json", strings.NewReader("{}"))
	if e != nil {
		log.Printf("获取许可证请求错误: %s", e.Error())
		over <- 0
	} else {
		// 获取到许可证过期时间
		b, _ := ioutil.ReadAll(res.Body)
		var data map[string]any
		json.Unmarshal(b, &data)
		d, ok := data["data"].(map[string]any)
		if ok {
			endTimeMs := d["endTimeMs"].(float64)
			endTimestamp := int64(endTimeMs / 1000)
			cacheEndTimestamp = float64(endTimestamp)
			over <- cacheEndTimestamp
		} else {
			log.Printf("获取许可证信息响应错误数据: %s", string(b))
			over <- 0
		}
	}
}

// 登录cloudcanal, 获取到cookie
func login(baseUrl string, username string, password string, over chan float64) {
	url := baseUrl + "/login"
	s := `{"email":"","password":"%s","ifLogin":false,"phone":"","username":"","company":"","code":"","verifyCode":"","account":"%s","loginType":"LOGIN_PASSWORD","passwordAgain":"","inviteCode":""}`
	payload := fmt.Sprintf(s, password, username)
	r, err := c.Post(url, "application/json", strings.NewReader(payload))
	if err != nil {
		if r != nil {
			log.Printf("登录错误: %s status_code: %d\n", err.Error(), r.StatusCode)
		} else {
			log.Printf("登录错误: %s\n", err.Error())
		}
		over <- 0
	} else {
		b, _ := ioutil.ReadAll(r.Body)
		var data map[string]any
		json.Unmarshal(b, &data)
		code, ok := data["code"].(string)
		if ok && code != "1" {
			log.Printf("登录错误,响应: %s\n", string(b))
			over <- 0
		}
	}
}

// 定时清除缓存  30分钟一次
func clearCache() {
	ticker := time.NewTicker(30 * time.Minute)
	for {
		select {
		case <-ticker.C:
			cacheEndTimestamp = 0
			log.Printf("clear cache...\n")
		}
	}
}

func main() {

	flag.Parse()

	log.Printf("listen-address: %s url: %s interval: %d", *addr, *url, *interval)
	reg := prometheus.NewRegistry()

	// clear cache
	go clearCache()

	// df command execute metrics
	dfGauge := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "lm_cloudcanal_license_expiry",
		Help: "Get cloudcanal license expiry timestamp",
	}, func() float64 {
		over := make(chan float64)
		go func(over chan float64) {
			if cacheEndTimestamp <= 0 {
				login(*url, *username, *password, over)
				getLicenseExpireMetrics(*url, over)
			} else {
				over <- cacheEndTimestamp
			}
		}(over)
		for {
			select {
			case <-timeout:
				{
					log.Printf("request timeout,retry next time...\n")
					return float64(0)
				}
			case res, _ := <-over:
				{
					log.Printf("license timestamp = %f", res)
					return res
				}
			}
		}
	})
	reg.MustRegister(dfGauge)

	// Expose the registered metrics via HTTP.
	http.Handle("/metrics", promhttp.HandlerFor(
		reg,
		promhttp.HandlerOpts{
			// Opt into OpenMetrics to support exemplars.
			EnableOpenMetrics: true,
		},
	))
	log.Fatal(http.ListenAndServe(*addr, nil))
}

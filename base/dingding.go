package base

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"time"
)

//将消息发送到钉钉
func PostDD(content, apiurl string) {
	msg := `{"msgtype": "text", "text": {"content": "$content$"}}`
	mgs1 := strings.Replace(msg, "$content$", content, -1)
	body := bytes.NewBuffer([]byte(mgs1))
	httpClient := http.Client{
		// Transport: &http.Transport{
		// 	Dial: func(netw, addr string) (net.Conn, error) {
		// 		c, err := net.DialTimeout(netw, addr, time.Second*3) //设置建立连接超时
		// 		if err != nil {
		// 			return nil, err
		// 		}
		// 		c.SetDeadline(time.Now().Add(120 * time.Second)) //设置发送接收数据超时
		// 		return c, nil
		// 	},
		// },
		Timeout: 120 * time.Second,
	}

	resp, err := httpClient.Post(apiurl, "application/json", body)
	if err != nil {
		fmt.Printf("推送钉钉 url:%v body:%v 发送错误:%v\n", apiurl, body, err)
		return
	}
	defer resp.Body.Close()
}

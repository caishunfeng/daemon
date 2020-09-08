package main

import (
	"daemon/base"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/360EntSecGroup-Skylar/excelize"
	"github.com/robfig/cron"

	"golang.org/x/crypto/ssh"
)

//应用守护结构体
type AppDaemon struct {
	ID                string //ID
	Name              string //应用名
	GrepMatch         string //应用查询匹配字符串
	InnerIP           string //应用部署内网IP地址
	OuterIP           string //应用部署外网IP地址
	Port              int    //连接端口
	User              string //远程登录用户名
	Password          string //远程登录密码
	StartCmd          string //启动命令
	ShutDownCmd       string //停止命令,若命令为空，则默认为kill
	IsExistCheck      bool   //是否需要检查存活
	IsExistExpress    string //检查是否存活定时表达式
	IsExistTaskStatus int    //任务执行状态,0为待执行,1为执行中
	// IsMoreCheck       bool   //是否需要检查重复
	// IsMoreExpress     string //检查是否重复定时表达式
	// IsMoreTaskStatus  int    //任务执行状态,0为待执行,1为执行中
	IsAlarm    bool   //是否需要钉钉预警
	DDAlarmUrl string //钉钉预警URL
}

var DDAlarmUrl string = ""
var Environment string = ""
var apps []AppDaemon
var maps map[string][]AppDaemon

func main() {
	args := os.Args
	if len(args) <= 1 {
		fmt.Println("请在启动时输入命令参数,参数一为监控系统别名，参数二为钉钉推送地址")
		return
	}
	Environment = args[1]
	DDAlarmUrl = args[2]

	cells, err := readExcel("./server.xlsx")
	if err != nil {
		fmt.Printf("读取excel异常,原因:%v\n", err)
		return
	}

	apps = []AppDaemon{}
	maps = make(map[string][]AppDaemon)
	for index, row := range cells {

		if index == 0 {
			continue
		}

		app := new(AppDaemon)
		app.ID = row[0]
		app.Name = row[1]
		app.GrepMatch = row[2]
		app.InnerIP = row[3]
		app.OuterIP = row[4]
		port, _ := strconv.Atoi(row[5])
		app.Port = port
		app.User = row[6]
		app.Password = row[7]
		app.StartCmd = row[8]
		app.ShutDownCmd = row[9]
		app.IsExistCheck = true
		app.IsExistExpress = "0/2 * * * * ?"
		app.IsExistTaskStatus = 0
		// app.IsMoreCheck = true
		// app.IsMoreExpress = "0/5 * * * * ?"
		// app.IsMoreTaskStatus = 0
		app.IsAlarm = true
		apps = append(apps, *app)

		if _, ok := maps[app.InnerIP]; ok {
			maps[app.InnerIP] = append(maps[app.InnerIP], *app)
		} else {
			maps[app.InnerIP] = []AppDaemon{}
			maps[app.InnerIP] = append(maps[app.InnerIP], *app)
		}
	}

	fmt.Printf("加载完成,总共有%d个应用\n", len(apps))

	isExistCron := cron.New()
	isExistCron.AddFunc("0 * * * * ?", start)
	isExistCron.Start()
	defer isExistCron.Stop()

	for {
		time.Sleep(10 * time.Second)
		fmt.Println("主线程保持存活")
	}

}

func readExcel(filePath string) ([][]string, error) {
	xlsx, err := excelize.OpenFile(filePath)
	if err != nil {
		fmt.Println(fmt.Sprintf("打开exccel文件失败,路径:%s,异常:%v", filePath, err))
		return nil, err
	}
	rows := xlsx.GetRows("Sheet1")
	for _, row := range rows {
		for _, colCell := range row {
			fmt.Printf("%s\t", colCell)
		}
		fmt.Println()
	}
	return rows, nil
}

func start() {
	fmt.Println("开始检测服务")
	for _, apps := range maps {
		go startCheckServer(apps)
	}
	fmt.Println("结束检测服务")
}

//远程连接
func connect(host, user, password string, port int) (*ssh.Client, error) {
	var (
		auth         []ssh.AuthMethod
		addr         string
		clientConfig *ssh.ClientConfig
		client       *ssh.Client
		err          error
	)
	auth = make([]ssh.AuthMethod, 0)
	auth = append(auth, ssh.Password(password))

	clientConfig = &ssh.ClientConfig{
		User:    user,
		Auth:    auth,
		Timeout: 30 * time.Second,
	}
	addr = fmt.Sprintf("%s:%d", host, port)
	client, err = ssh.Dial("tcp", addr, clientConfig)
	if err != nil {
		return nil, err
	}
	return client, nil
}

//开启服务检测
func startCheckServer(apps []AppDaemon) {

	fmt.Printf("准备连接%s,%s,%s,%d\n", apps[0].InnerIP, apps[0].User, apps[0].Password, apps[0].Port)

	client, err := connect(apps[0].InnerIP, apps[0].User, apps[0].Password, apps[0].Port)
	if err != nil {
		fmt.Printf("远程连接异常,原因:%v\n", err)
		base.PostDD(fmt.Sprintf("远程连接%s的%s异常,请检查服务器", Environment, apps[0].OuterIP), DDAlarmUrl)
		return
	}
	defer client.Close()

	for _, app := range apps {
		if app.IsExistTaskStatus == 1 {
			fmt.Printf("%s还在执行中,本次执行跳过\n", app.Name)
			continue
		}

		app.IsExistTaskStatus = 1
		defer func() {
			app.IsExistTaskStatus = 0
		}()

		checkCmd := fmt.Sprintf("ps -ef |grep %s | awk  'BEGIN{OFS=\":\"}{print $2,$3,$8}'", app.Name)

		checkResult, err := runCmd(client, checkCmd)
		if err != nil {
			fmt.Printf("执行命令%s出错:%v\n", checkCmd, err)
			continue
		}
		appCount, _, err := checkAppCount(app.GrepMatch, checkResult)
		if err != nil {
			fmt.Printf("分析应用%s数量出错,分析内容:%s,出错:%v\n", app.GrepMatch, checkResult, err)
			continue
		}

		if appCount == 1 {
			continue
		}

		base.PostDD(fmt.Sprintf("监测到%s的%s上有应用%s个数:%d", Environment, app.OuterIP, app.Name, appCount), DDAlarmUrl)

		if app.StartCmd == "NULL" {
			fmt.Printf("%s上的应用%s没有重启命令,需联系发布同学重启\n", app.InnerIP, app.Name)
			continue
		}

		if appCount == 0 {
			//如果数量为0,有可能正在重启,此处暂停10s,再检查一遍

			//暂停10s
			time.Sleep(40 * time.Second)

			//重新检查一遍
			checkResult2, err := runCmd(client, checkCmd)
			if err != nil {
				fmt.Printf("执行命令%s出错:%v\n", checkCmd, err)
				continue
			}
			appCount2, _, err := checkAppCount(app.GrepMatch, checkResult2)
			if err != nil {
				fmt.Printf("分析应用%s数量出错,分析内容:%s,出错:%v\n", app.GrepMatch, checkResult2, err)
				continue
			}

			base.PostDD(fmt.Sprintf("暂停40秒后重新监测到%s的%s上有应用%s个数:%d", Environment, app.OuterIP, app.Name, appCount2), DDAlarmUrl)

			//如果还是为0,则重启
			if appCount2 == 0 {
				//重启
				startCmd := app.StartCmd
				startResult, err := runCmd(client, startCmd)
				if err != nil {
					fmt.Printf("执行命令%s失败,原因:%v\n", startCmd, err)
					base.PostDD(fmt.Sprintf("启动%s(ip:%s)应用%s失败,原因:%v", Environment, app.OuterIP, app.Name, err), DDAlarmUrl)
					return
				}

				fmt.Printf("执行命令%s成功,返回结果:%s\n", startCmd, startResult)

				base.PostDD(fmt.Sprintf("启动%s(ip:%s)应用%s成功", Environment, app.OuterIP, app.Name), DDAlarmUrl)
			}

		} else if appCount > 1 {
			//如果数量大于1则杀掉进程并暂停10s
			shutdownCmd := fmt.Sprintf("kill -9 `ps -ef | grep %s | grep -v grep | awk '{print $2}'`", app.Name)
			shutdownResult, err := runCmd(client, shutdownCmd)
			if err != nil {
				fmt.Printf("执行命令%s失败,原因:%v\n", shutdownCmd, err)
				base.PostDD(fmt.Sprintf("杀掉%s(ip:%s)应用%s失败,原因:%v", Environment, app.OuterIP, app.Name, err), DDAlarmUrl)
				return
			}
			fmt.Printf("执行命令%s成功,返回结果:%s\n", shutdownCmd, shutdownResult)

			base.PostDD(fmt.Sprintf("关闭%s(ip:%s)应用%s成功", Environment, app.OuterIP, app.Name), DDAlarmUrl)

			//暂停10s
			time.Sleep(10 * time.Second)

			//重启
			startCmd := app.StartCmd
			startResult, err := runCmd(client, startCmd)
			if err != nil {
				fmt.Printf("执行命令%s失败,原因:%v\n", startCmd, err)
				base.PostDD(fmt.Sprintf("启动%s(ip:%s)应用%s失败,原因:%v", Environment, app.OuterIP, app.Name, err), DDAlarmUrl)
				return
			}
			fmt.Printf("执行命令%s成功,返回结果:%s\n", startCmd, startResult)
			base.PostDD(fmt.Sprintf("启动%s(ip:%s)应用%s成功", Environment, app.OuterIP, app.Name), DDAlarmUrl)
		}
	}
}

func runCmd(client *ssh.Client, cmd string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		fmt.Printf("创建session异常,原因:%v\n", err)
		return "", err
	}
	defer session.Close()

	bytes, err := session.Output(cmd)
	if err != nil {
		fmt.Printf("执行命令%s失败,异常:%v\n", cmd, err)
		return "", err
	}

	fmt.Printf("执行命令%s成功,返回结果:%s\n", cmd, string(bytes))

	return string(bytes), nil

}

//检查应用存活的数量,返回存活数量以及进程集合map[pid]ppid
func checkAppCount(grepMatch, out string) (int, map[int]int, error) {
	count := 0
	pids := make(map[int]int)
	lines := strings.Split(string(out), "\n")

	for _, line := range lines {
		strs := strings.Split(line, ":")
		if len(strs) < 3 {
			continue
		}
		if strings.Contains(strs[2], grepMatch) {
			pid, err := strconv.Atoi(strs[0])
			if err != nil {
				fmt.Printf("进程号转换报错,pid:%s,原因:%v\n", strs[0], err)
				continue
			}
			ppid, err := strconv.Atoi(strs[1])
			if err != nil {
				fmt.Printf("父进程号转换报错,ppid:%s,原因:%v\n", strs[1], err)
				continue
			}
			fmt.Printf("获取的pid:%d,ppid:%d\n", pid, ppid)
			count++
			pids[pid] = ppid
		}
	}
	fmt.Printf("一共检查到web应用有%d个\n", count)
	return count, pids, nil
}

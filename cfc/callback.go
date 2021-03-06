package main

import (
	"io/ioutil"
	"os"
	"owl/common/types"
	"path/filepath"

	"github.com/wuyingsong/tcp"
	"github.com/wuyingsong/utils"
)

type callback struct {
}

func (cb *callback) OnConnected(conn *tcp.TCPConn) {
	lg.Info("callback:%s connected", conn.GetRemoteAddr().String())
}

//链接断开回调
func (cb *callback) OnDisconnected(conn *tcp.TCPConn) {
	lg.Info("callback:%s disconnect ", conn.GetRemoteAddr().String())
}

//错误回调
func (cb *callback) OnError(err error) {
	lg.Error("callback: %s", err)
}

//消息处理回调
func (cb *callback) OnMessage(conn *tcp.TCPConn, p tcp.Packet) {
	defer func() {
		if r := recover(); r != nil {
			lg.Error("Recovered in OnMessage", r)
		}
	}()
	pkt := p.(*tcp.DefaultPacket)
	lg.Debug("%s, %s", types.MsgTextMap[pkt.Type], string(pkt.Body))
	cb.dispatch(conn, pkt)
}

func (cb *callback) dispatch(conn *tcp.TCPConn, pkt *tcp.DefaultPacket) {
	switch pkt.Type {
	// 客户端注册
	case types.MsgAgentRegister:
		host := &types.Host{}
		if err := host.Decode(pkt.Body); err != nil {
			lg.Error("decode host error", err)
			conn.Close()
			return
		}
		if err := mydb.createOrUpdateHost(host); err != nil {
			lg.Error("register agent error:%s, host:%v", err, host)
			return
		}
		lg.Debug("register agent:%v", host)
		// 客户端上传metric

	case types.MsgAgentSendMetricInfo:
		metricConfig := types.MetricConfig{}
		// 反序列化
		if err := metricConfig.Decode(pkt.Body); err != nil {
			lg.Error("decode metricConfig error", err)
			return
		}
		if metricConfig.HostID == "" {
			hostname := metricConfig.SeriesData.Tags["host"]
			metricConfig.HostID = getHostIDByHostname(hostname)
		}
		metricConfig.SeriesData.RemoveTag("host")
		metricConfig.SeriesData.RemoveTag("uuid")
		//创建 metric
		if err := mydb.createOrUpdateMetric(
			metricConfig.HostID,
			metricConfig.SeriesData,
		); err != nil {
			lg.Error("create or update metric error %s metric:%v", err, metricConfig)
			return
		}
		lg.Debug("create metric %s", metricConfig.Encode())
	// 客户端获取需要执行的插件列表
	case types.MsgAgentGetPluginsList:
		var (
			err  error
			host types.Host
		)
		// 反序列化
		if err = host.Decode(pkt.Body); err != nil {
			lg.Error("decode host error %s", err)
			conn.Close()
			return
		}
		// 获取 plugin
		plugins, err := mydb.getHostPlugins(host.ID)
		if err != nil {
			lg.Error("get host plugin error %s host:%v", err, host)
			return
		}
		resp := types.GetPluginResp{
			HostID:  host.ID,
			Plugins: plugins,
		}
		// 发送结果集到 agent
		if err = conn.AsyncWritePacket(
			tcp.NewDefaultPacket(
				types.MsgCFCSendPluginsList,
				resp.Encode(),
			),
		); err != nil {
			lg.Error("send plugin list to agent error %s", err)
		}
	case types.MsgAgentRequestSyncPlugins:
		spr := types.SyncPluginRequest{}
		if err := spr.Decode(pkt.Body); err != nil {
			lg.Error("decode SyncPluginRequest error", err)
			return
		}
		pth := filepath.Join(GlobalConfig.PluginDir, spr.Path)
		md5String, err := utils.GetFileMD5(pth)
		if err != nil {
			lg.Error("get plugin(%s) checksum error(%s)", spr.Path, err)
			return
		}
		if md5String != spr.Checksum {
			lg.Error("%s checksum verification failed, want(%s) have(%s)", spr.Path, spr.Checksum, md5String)
			return
		}
		fd, err := os.Open(pth)
		if err != nil {
			lg.Error("%s", err)
			return
		}
		defer fd.Close()
		sp := types.SyncPluginResponse{
			HostID: spr.HostID,
			Path:   spr.Path,
		}
		fileBytes, err := ioutil.ReadAll(fd)
		if err != nil {
			lg.Error("%s", err)
			return
		}
		sp.Body = fileBytes
		conn.AsyncWritePacket(tcp.NewDefaultPacket(
			types.MsgCFCSendPlugin,
			sp.Encode(),
		))

	case types.MsgAgentSendHeartbeat:
		host := new(types.Host)
		if err := host.Decode(pkt.Body); err != nil {
			lg.Error("decode host error %s", err)
			return
		}
		if host.ID == "" {
			lg.Warning("host id is empty %v", host)
			return
		}
		mydb.createOrUpdateHost(host)
	default:
		lg.Warn("%v no callback", types.MsgTextMap[pkt.Type])
	}
}

func getHostIDByHostname(hostname string) string {
	host, err := mydb.getHostByHostname(hostname)
	if err != nil {
		lg.Error("get host id by hostname failed, hostname:%q, error:%s", hostname, err)
	}
	if host == nil {
		return ""
	}
	return host.ID
}

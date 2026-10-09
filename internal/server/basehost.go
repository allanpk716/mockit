package server

// 对外基址主机的自动探测(票 09/D16 三层之第二层)。
//
// external_url 未配置时,探测本机网卡上落在 100.64.0.0/10(NetBird 所在的
// CGNAT 段)的 IPv4:唯一命中才采用;零命中或多命中(歧义)一律返回空——
// server 照常启动、页面照常可审,仅 submit 在基址不可定时明确报错。
// 探测时机 = serve 绑定端口写实例锁时,结果随 lock.base_host 落盘;
// MCP 只读锁,不自探。

import "net"

// netbirdNet 是探测网段 100.64.0.0/10(CGNAT 共享段,NetBird 默认落此)。
var netbirdNet = &net.IPNet{
	IP:   net.IPv4(100, 64, 0, 0),
	Mask: net.CIDRMask(10, 32),
}

// interfaceAddrs 枚举本机网卡地址(跳过 down 与 loopback 接口);
// 包级变量供测试注入——单测不得依赖真实网卡。
var interfaceAddrs = func() ([]net.IP, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []net.IP
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue // 单块网卡枚举失败不拖垮整体探测
		}
		for _, a := range addrs {
			switch v := a.(type) {
			case *net.IPNet:
				out = append(out, v.IP)
			case *net.IPAddr:
				out = append(out, v.IP)
			}
		}
	}
	return out, nil
}

// detectBaseHost 返回探测到的对外主机名:100.64.0.0/10 内唯一命中的
// IPv4 字面量;零命中/多命中/枚举失败返回 ""(歧义与空等同,不猜)。
func detectBaseHost() string {
	addrs, err := interfaceAddrs()
	if err != nil {
		return ""
	}
	return pickNetBirdHost(addrs)
}

// pickNetBirdHost 在地址清单里找 100.64.0.0/10 的唯一 IPv4 命中;
// 多于一个命中即歧义,返回 ""(不留任何一边)。
func pickNetBirdHost(addrs []net.IP) string {
	var hit string
	for _, ip := range addrs {
		ip4 := ip.To4()
		if ip4 == nil {
			continue // 规格只认 IPv4
		}
		if !netbirdNet.Contains(ip4) {
			continue
		}
		if hit != "" {
			return ""
		}
		hit = ip4.String()
	}
	return hit
}

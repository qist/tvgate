package stream

// FCC Protocol Type - Based on vendor and port
const (
	FCC_TYPE_TELECOM = 0 // Telecom/ZTE/Fiberhome
	FCC_TYPE_HUAWEI  = 1 // Huawei
)

// FCC Timeout Configuration
const (
	FCC_TIMEOUT_SIGNALING_MS  = 80  // 信令阶段超时（FCC_STATE_REQUESTED或FCC_STATE_UNICAST_PENDING）
	FCC_TIMEOUT_UNICAST_SEC   = 1.0 // 单播媒体包超时（FCC_STATE_UNICAST_ACTIVE）
	FCC_TIMEOUT_SYNC_WAIT_SEC = 15.0 // 等待服务器同步通知的最大时间
)

// FCC State Machine - Based on Fast Channel Change Protocol
const (
	FCC_STATE_INIT           = 0 // 初始状态
	FCC_STATE_REQUESTED      = 1 // 已发送FCC请求，等待服务器响应
	FCC_STATE_UNICAST_PENDING = 2 // 服务器已接受，等待第一个单播包
	FCC_STATE_UNICAST_ACTIVE = 3 // 正在接收FCC单播流
	FCC_STATE_MCAST_REQUESTED = 4 // 通知服务器加入组播，正在转换中
	FCC_STATE_MCAST_ACTIVE   = 5 // 已完全切换到组播接收
	FCC_STATE_ERROR          = 6 // 错误状态
)

// FCC RTCP Packet Format Types
const (
	// Telecom FCC Packet Types
	FCC_FMT_TELECOM_REQ  = 2 // 电信FCC请求包 (FMT 2)
	FCC_FMT_TELECOM_RESP = 3 // 电信FCC响应包 (FMT 3)
	FCC_FMT_TELECOM_SYNC = 4 // 电信FCC同步通知 (FMT 4)
	FCC_FMT_TELECOM_TERM = 5 // 电信FCC终止包 (FMT 5)

	// Huawei FCC Packet Types
	FCC_FMT_HUAWEI_REQ  = 5  // 华为FCC请求包 (FMT 5)
	FCC_FMT_HUAWEI_RESP = 6  // 华为FCC响应包 (FMT 6)
	FCC_FMT_HUAWEI_NAT  = 12 // 华为FCC NAT穿越包 (FMT 12)
	FCC_FMT_HUAWEI_SYNC = 8  // 华为FCC同步通知 (FMT 8)
	FCC_FMT_HUAWEI_TERM = 9  // 华为FCC终止包 (FMT 9)
)
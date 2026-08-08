package core

// In-band packet types
const (
	typeSessionDummy = iota // nolint:deadcode,varcheck
	typeSessionTraffic
	typeSessionProto
	typeSessionGarlic // optional experimental Garlic Routing Overlay, see src/garlic
)

// Protocol packet types
const (
	typeProtoDummy = iota
	typeProtoNodeInfoRequest
	typeProtoNodeInfoResponse
	typeProtoDebug = 255
)

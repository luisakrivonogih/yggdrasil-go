package admin

import (
	"encoding/hex"

	"github.com/yggdrasil-network/yggdrasil-go/src/version"
)

type GetSelfRequest struct{}

type GetSelfResponse struct {
	BuildName      string  `json:"build_name"`
	BuildVersion   string  `json:"build_version"`
	PublicKey      string  `json:"key"`
	IPAddress      string  `json:"address"`
	RoutingEntries uint64  `json:"routing_entries"`
	Subnet         string  `json:"subnet"`
	Uptime         float64 `json:"uptime"`
}

func (a *AdminSocket) getSelfHandler(_ *GetSelfRequest, res *GetSelfResponse) error {
	self := a.core.GetSelf()
	snet := a.core.Subnet()
	res.BuildName = version.BuildName()
	res.BuildVersion = version.BuildVersion()
	res.PublicKey = hex.EncodeToString(self.Key[:])
	res.IPAddress = a.core.Address().String()
	res.Subnet = snet.String()
	res.RoutingEntries = self.RoutingEntries
	res.Uptime = a.core.Uptime().Seconds()
	return nil
}

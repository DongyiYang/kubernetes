package connectioncounter

type Connection struct {
	ServiceId           string             `json:"serviceID,omitempty"`
	EndpointsCounterMap map[string]float64 `json:"endpointCounter,omitempty"`
	EpCountAbs          map[string]int     `json:"endpointAbs,omitempty"`
}

func (this *Connection) GetEndpointsCounterMap() map[string]float64 {
	return this.EndpointsCounterMap
}

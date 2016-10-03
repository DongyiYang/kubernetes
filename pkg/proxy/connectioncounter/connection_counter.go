package connectioncounter

import (
	"sync"
	"time"

	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/types"

	"k8s.io/kubernetes/pkg/util/conntrack"

	"github.com/golang/glog"
)

type endpointsInfo struct {
	types.NamespacedName
}

type ConnectionCounter struct {
	conntrack *conntrack.ConnTrack

	mu sync.Mutex

	endpointsMap map[string]*endpointsInfo

	// key is service name, value is the connection related to it.
	counter map[string]map[string]int

	lastPollTimestamp uint64
}

func NewConnectionCounter(conntrack *conntrack.ConnTrack) *ConnectionCounter {
	return &ConnectionCounter{
		counter:   make(map[string]map[string]int),
		conntrack: conntrack,

		endpointsMap: make(map[string]*endpointsInfo),
	}
}

// Implement k8s.io/pkg/proxy/config/EndpointsConfigHandler Interface.
func (this *ConnectionCounter) OnEndpointsUpdate(allEndpoints []api.Endpoints) {
	start := time.Now()
	defer func() {
		glog.V(4).Infof("OnEndpointsUpdate took %v for %d endpoints", time.Since(start), len(allEndpoints))
	}()

	this.mu.Lock()
	defer this.mu.Unlock()

	// Clear the current endpoints set.
	this.endpointsMap = make(map[string]*endpointsInfo)

	for i := range allEndpoints {
		endpoints := &allEndpoints[i]
		for j := range endpoints.Subsets {
			ss := &endpoints.Subsets[j]
			for k := range ss.Addresses {
				addr := &ss.Addresses[k]
				this.endpointsMap[addr.IP] = &endpointsInfo{types.NamespacedName{Namespace: endpoints.Namespace, Name: endpoints.Name}}
			}
		}
	}

	this.syncConntrack()
}

// Clear the connection counter map.
func (tc *ConnectionCounter) Reset() {
	glog.V(3).Infof("Reset connection counter")
	counterMap := make(map[string]map[string]int)

	tc.counter = counterMap

	// As after each poll, the counter map is cleaned, so this is the right place to set the lastPollTimestamp.
	tc.lastPollTimestamp = uint64(time.Now().Unix())
}

// Increment the connection count for a single endpoint.
// Connection counter map uses serviceName as key and endpoint map as value.
// In endpoint map, key is endpoint IP address, value is the number of connection happened on the endpoint.
func (tc *ConnectionCounter) Count(infos []*countInfo) {
	for _, info := range infos {
		serviceName := info.serviceName
		endpointAddress := info.endpointAddress
		epMap, ok := tc.counter[serviceName]
		if !ok {
			glog.V(4).Infof("Service %s is not tracked. Now initializing in map", serviceName)

			epMap = make(map[string]int)
		}
		count, ok := epMap[endpointAddress]
		if !ok {
			glog.V(4).Infof("Endpoint %s for Service %s is not tracked. Now initializing in map", endpointAddress, serviceName)
			count = 0
		}
		epMap[endpointAddress] = count + 1
		tc.counter[serviceName] = epMap
		glog.V(4).Infof("Connection count of %s is %d.", endpointAddress, epMap[endpointAddress])
	}
}

func (tc *ConnectionCounter) GetAllConnections() []*Connection {
	var connections []*Connection

	// Here we need to translate the absolute count value into count/second.
	if tc.lastPollTimestamp == 0 {
		// When lastPollTimestamp is 0, meaning that current poll is the first poll. We cannot get the count/s, so just return.
		return connections
	}
	// Get the time difference between two poll.
	timeDiff := uint64(time.Now().Unix()) - tc.lastPollTimestamp
	glog.V(4).Infof("Time diff is %d", timeDiff)

	for svcName, epMap := range tc.counter {
		// Before append, change count to count per second.
		valueMap := make(map[string]float64)
		countMap := make(map[string]int)
		for ep, count := range epMap {
			valueMap[ep] = float64(count) / float64(timeDiff)
			countMap[ep] = count
		}
		connection := &Connection{
			ServiceId:           svcName,
			EndpointsCounterMap: valueMap,
			EpCountAbs:          countMap,
		}
		glog.V(4).Infof("Get connection data: %++v", connection)
		connections = append(connections, connection)
	}

	return connections
}

// Get all the current Established TCP connections from conntrack and add count to connection counter.
func (this *ConnectionCounter) ProcessConntrackConnections() {
	this.mu.Lock()
	defer this.mu.Unlock()

	this.syncConntrack()
}

func (this *ConnectionCounter) syncConntrack() {
	connections := this.conntrack.Connections()
	if len(connections) > 0 {
		glog.V(4).Infof("Connections:\n")
		for _, cn := range connections {
			infos := this.preProcessConnections(cn)
			this.Count(infos)
		}
	}
}

type countInfo struct {
	serviceName     string
	endpointAddress string
}

// Fileter out connection does not have endpoints address as either Local or Remote Address
func (this *ConnectionCounter) preProcessConnections(c conntrack.TCPConnection) []*countInfo {
	var infos []*countInfo
	if svcName, exist := this.endpointsMap[c.Local]; exist {
		infos = append(infos, &countInfo{svcName.String(), c.Local})
	}
	if svcName, exist := this.endpointsMap[c.Remote]; exist {
		infos = append(infos, &countInfo{svcName.String(), c.Remote})
	}
	return infos
}

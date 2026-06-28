package agentlink

import (
	"encoding/json"

	"lotsman/internal/agentlink/pb"
)

// marshalSignal encodes an Event's model.Signal as the JSON payload carried in
// pb.Event.signal (the inverse of gatewayLink.dispatchEvent).
func marshalSignal(ev Event) ([]byte, error) {
	return json.Marshal(ev.Signal)
}

// kindToProto maps the transport-neutral RequestKind onto the wire enum. Keeping
// this in one place means the proto enum never leaks past the agentlink package:
// the engine, remote proxy, and agent handler all speak the string RequestKind.
var kindToProto = map[RequestKind]pb.RequestKind{
	ReqQueryLogs:       pb.RequestKind_QUERY_LOGS,
	ReqQueryMetrics:    pb.RequestKind_QUERY_METRICS_INSTANT,
	ReqQueryRange:      pb.RequestKind_QUERY_METRICS_RANGE,
	ReqChangeEvents:    pb.RequestKind_CHANGE_EVENTS,
	ReqK8sEvents:       pb.RequestKind_K8S_EVENTS,
	ReqListWorkloads:   pb.RequestKind_LIST_WORKLOADS,
	ReqListNodes:       pb.RequestKind_LIST_NODES,
	ReqListPods:        pb.RequestKind_LIST_PODS,
	ReqPodLogs:         pb.RequestKind_POD_LOGS,
	ReqListConfigMaps:  pb.RequestKind_LIST_CONFIGMAPS,
	ReqGetConfigMap:    pb.RequestKind_GET_CONFIGMAP,
	ReqListSecrets:     pb.RequestKind_LIST_SECRETS,
	ReqGetSecret:       pb.RequestKind_GET_SECRET,
	ReqWorkloadHistory: pb.RequestKind_WORKLOAD_HISTORY,
}

// kindFromProto is the inverse of kindToProto, built once at init.
var kindFromProto = func() map[pb.RequestKind]RequestKind {
	m := make(map[pb.RequestKind]RequestKind, len(kindToProto))
	for k, v := range kindToProto {
		m[v] = k
	}
	return m
}()

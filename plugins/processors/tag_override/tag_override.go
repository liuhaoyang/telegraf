package tagoverride

import (
	"strconv"
	"strings"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/filter"
	"github.com/influxdata/telegraf/plugins/inputs/global/node"
	"github.com/influxdata/telegraf/plugins/processors"
)

const sampleConfig = `
  override = true
`

var mapping = map[string]string{
	"DICE_ORG":                    "org_id",
	"DICE_ORG_ID":                 "org_id",
	"DICE_ORG_NAME":               "org_name",
	"DICE_PROJECT":                "project_id",
	"DICE_PROJECT_NAME":           "project_name",
	"DICE_APPLICATION":            "application_id",
	"DICE_APPLICATION_NAME":       "application_name",
	"DICE_RUNTIME":                "runtime_id",
	"DICE_RUNTIME_NAME":           "runtime_name",
	"DICE_SERVICE":                "service_name",
	"DICE_COMPONENT":              "component_name",
	"DICE_ADDON":                  "addon_name",
	"DICE_WORKSPACE":              "workspace",
	"DICE_CPU_REQUEST":            "cpu_request",
	"DICE_CPU_LIMIT":              "cpu_limit",
	"DICE_CPU_ORIGIN":             "cpu_origin",
	"DICE_MEM_REQUEST":            "mem_request",
	"DICE_MEM_LIMIT":              "mem_limit",
	"DICE_MEM_ORIGIN":             "mem_origin",
	"TERMINUS_DEFINE_TAG":         "job_id",
	"ADDON_ID":                    "addon_id",
	"MESOS_TASK_ID":               "instance_id",
	"EDAS_APP_ID":                 "edas_app_id",
	"EDAS_APP_NAME":               "edas_app_name",
	"EDAS_GROUP_ID":               "edas_group_id",
	"io.kubernetes.pod.name":      "pod_name",
	"io.kubernetes.pod.namespace": "pod_namespace",
	"io.kubernetes.pod.uid":       "pod_uid",
	"POD_IP":                      "pod_ip",
	"TERMINUS_KEY":                "terminus_key",
}

// TagOverride .
type TagOverride struct {
	Override bool `toml:"override"`

	prefixFilter filter.Filter
	init         bool
}

// Description .
func (*TagOverride) Description() string {
	return "Convert the value of the environment variable injected into the container by dice."
}

// SampleConfig .
func (*TagOverride) SampleConfig() string {
	return sampleConfig
}

// Apply .
func (t *TagOverride) Apply(in ...telegraf.Metric) []telegraf.Metric {
	t.initOnce()

	info := node.GetInfo()
	var orgName string
	labels, err := node.GetLabels()
	if err == nil {
		orgName = labels.OrgName()
	}
	for _, metric := range in {
		if strings.HasPrefix(metric.Name(), "docker_container_") {
			t.modifyDockerContainerTags(metric)
		}

		var hostNum string
		podName, hasPodName := metric.GetTag("io.kubernetes.pod.name")
		if hasPodName {
			if podName != "" {
				idx := strings.LastIndex(podName, "-")
				if idx > 0 {
					_, err := strconv.Atoi(podName[idx+1:])
					if err == nil {
						hostNum = podName[idx+1:]
					}
				}
			}
		}
		var serviceName, instanceType, runtimeName, applicationId string
		for k, val := range metric.Tags() {
			if t.prefixFilter.Match(k) {
				metric.RemoveTag(k)
				prefix := "N" + hostNum + "_"
				if !strings.HasPrefix(k, prefix) {
					continue
				}
				k = k[len(prefix):]
				metric.AddTag(k, val)
			}
			if n, has := mapping[k]; has {
				metric.AddTag(n, val)
				if t.Override {
					metric.RemoveTag(k)
				}
				switch n {
				case "job_id":
					instanceType = "job"
				case "addon_name":
					instanceType = "addon"
				case "component_name":
					instanceType = "component"
				case "runtime_id":
					instanceType = "service"
				case "service_name":
					serviceName = val
				case "runtime_name":
					instanceType = "service"
					runtimeName = val
				case "application_id":
					applicationId = val
				}
			} else {
				if strings.HasPrefix(k, "DICE_") {
					key := strings.ToLower(k[len("DICE_"):])
					if _, ok := metric.GetTag(key); !ok {
						metric.AddTag(key, val)
					}
					if t.Override {
						metric.RemoveTag(k)
					}
				}
			}
		}
		if applicationId != "" && runtimeName != "" && serviceName != "" {
			metric.AddTag("service_id", strings.Join([]string{applicationId, runtimeName, serviceName}, "_"))
		}
		if instanceType != "" {
			metric.AddTag("instance_type", instanceType)
		}

		if info != nil {
			if len(info.ClusterName()) > 0 {
				if metric.HasTag("cluster_name") {
					metric.RemoveTag("cluster_name")
				}
				metric.AddTag("cluster_name", info.ClusterName())
			}
			if len(info.HostIP()) > 0 {
				if metric.HasTag("host_ip") {
					metric.RemoveTag("host_ip")
				}
				metric.AddTag("host_ip", info.HostIP())
			}
		}

		namespace, _ := metric.GetTag("pod_namespace")
		if namespace == "" && strings.Contains(metric.Name(), "kubernetes") {
			namespace, _ = metric.GetTag("namespace")
		}
		if namespace == "default" {
			t.modifyDiceAddonTags(metric)
		}
		t.setOrgTags(metric, orgName)
	}
	return in
}

func (t *TagOverride) modifyDockerContainerTags(metric telegraf.Metric) {
	containerIDKey := "container_id"
	value, ok := metric.GetField(containerIDKey)
	if ok {
		metric.AddTag(containerIDKey, value.(string))
	}

	metric.RemoveTag("host")
	metric.RemoveTag("engine_host")
	metric.RemoveTag("server_version")
	metric.RemoveTag("container_name")
	// metric.RemoveTag("container_image")
	metric.RemoveTag("container_version")
	metric.RemoveTag("container_status")
}

func (t *TagOverride) modifyDiceAddonTags(metric telegraf.Metric) {
	// 临时向前兼容方案
	// 未来 dice 的 addon 的 应有对应的 环境变量标示
	addonType, _ := metric.GetTag("addon_type")
	if addonType == "" {
		name, _ := metric.GetTag("pod_name")
		if name == "" {
			name, _ = metric.GetTag("deployment_name")
		}
		if name == "" {
			name, _ = metric.GetTag("statefulset_name")
		}
		if name == "" {
			name, _ = metric.GetTag("daemonset_name")
		}
		idx := strings.Index(name, "addon-")
		if idx >= 0 {
			addonType = name[idx+len("addon-"):]
			idx = strings.Index(addonType, "-")
			if idx >= 0 {
				addonType = addonType[0:idx]
			}
			metric.AddTag("addon_type", addonType)
		}
	}
	addonID, _ := metric.GetTag("addon_id")
	if addonType != "" && addonID == "" {
		metric.AddTag("addon_id", addonType)
	}
	if addonType != "" {
		metric.AddTag("instance_type", "addon")
		metric.RemoveTag("component_name")
	}
}

func (t *TagOverride) setOrgTags(metric telegraf.Metric, orgName string) {
	_, ok := metric.GetTag("org_name")
	if ok {
		return
	}
	if len(orgName) > 0 {
		metric.AddTag("org_name", orgName)
	}
}

func (t *TagOverride) initOnce() {
	if t.init {
		return
	}
	var prefix = []string{"N?_DICE_*", "N?_ADDON_*"}
	prefixFilter, err := filter.Compile(prefix)
	if err == nil {
		t.prefixFilter = prefixFilter
	}
	t.init = true
}

func init() {
	processors.Add("tag_override", func() telegraf.Processor {
		return &TagOverride{Override: true}
	})
}

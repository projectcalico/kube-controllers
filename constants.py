# Logging constants.
LOG_FORMAT = '%(asctime)s %(process)d %(levelname)s %(message)s'

# Default Kubernetes API value.
DEFAULT_API = "https://kubernetes.default:443"

# Path to the CA certificate (if it exists).
CA_CERT_PATH = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"

# Resource types.
RESOURCE_TYPE_NETWORK_POLICY = "NetworkPolicy"
RESOURCE_TYPE_POD = "Pod"
RESOURCE_TYPE_NAMESPACE = "Namespace"

# API paths to NetworkPolicy objects.
BETA_API = "%s/apis/extensions/v1beta1"
NET_POLICY_PATH = BETA_API + "/networkpolicies"
NET_POLICY_WATCH_PATH = BETA_API + "/watch/networkpolicies"

# Mapping of resource to api URL.
GET_URLS = {RESOURCE_TYPE_POD: "%s/api/v1/pods",
            RESOURCE_TYPE_NAMESPACE: "%s/api/v1/namespaces",
            RESOURCE_TYPE_NETWORK_POLICY: NET_POLICY_PATH}
WATCH_URLS = {RESOURCE_TYPE_POD: "%s/api/v1/watch/pods",
              RESOURCE_TYPE_NAMESPACE: "%s/api/v1/watch/namespaces",
              RESOURCE_TYPE_NETWORK_POLICY: NET_POLICY_WATCH_PATH}

# Annotation to look for network-isolation on namespaces.
NS_POLICY_ANNOTATION = "net.beta.kubernetes.io/network-policy"

# Tier name /order to use for policies.
NET_POL_TIER_ORDER = 1000

# The priority assigned to network policies created by the controller.
# Lower order -> higher priority.
NET_POL_ORDER = int(os.getenv("NET_POL_ORDER", 1000))

# The priority used for policies created by Namespaces.
NET_POL_NAMESPACE_ORDER = None
if os.getenv("NAMESPACE_POLICY_ORDER"):
    NET_POL_NAMESPACE_ORDER = int(os.getenv("NAMESPACE_POLICY_ORDER"))

# Environment variables for getting the Kubernetes API.
K8S_SERVICE_PORT = "KUBERNETES_SERVICE_PORT"
K8S_SERVICE_HOST = "KUBERNETES_SERVICE_HOST"

# Label which represents the namespace a given pod belongs to.
K8S_NAMESPACE_LABEL = "calico/k8s_ns"

# Format to use for namespace profile names.
NS_PROFILE_FMT = "k8s_ns.%s"

# Format to use for namespace policy names.
NS_POLICY_FMT = "ns.projectcalico.org/%s"

# Format to use for labels inherited from a namespace.
NS_LABEL_KEY_FMT = "k8s_ns/label/%s"

# Max number of updates to queue.
# Assuming 100 pods per host, 1000 hosts, we may queue
# about 100,000 updates at start of day.
MAX_QUEUE_SIZE = 100000

# Seconds to wait when adding to a full queue.
# It should easily not take more than a second to complete processing of
# an event off the queue.  Allow for five times that much to be safe.
QUEUE_PUT_TIMEOUT = 5

# Update types.
TYPE_ADDED = "ADDED"
TYPE_MODIFIED = "MODIFIED"
TYPE_DELETED = "DELETED"
TYPE_ERROR = "ERROR"

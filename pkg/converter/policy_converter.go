package converter

import (
	"errors"
	"fmt"
	"github.com/projectcalico/libcalico-go/lib/api"
	"github.com/projectcalico/libcalico-go/lib/numorstring"
	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/client-go/pkg/api/v1"
	netv1 "k8s.io/client-go/pkg/apis/networking/v1"
	//"reflect"
	"strings"
)

type policyConverter struct {
	k8sPolicy *netv1.NetworkPolicy
}

// Format used by calico for selectors by calico
const calicoSelectorFormat = "%s == '%s'"

// Format to use for labels inherited from a namespace.
const namespaceLabelKeyFormat = "k8s_ns/label/%s"

// The priority assigned to network policies created by the controller.
// Lower order -> higher priority.
const netPolOrder float64 = 1000

// NewPolicyConverter Constructor for policyConverter
func NewPolicyConverter() Converter {
	return &policyConverter{}
}
func (p *policyConverter) Convert(k8sObj interface{}) (interface{}, error) {
	//if reflect.TypeOf(k8sObj) != reflect.TypeOf(netv1.NetworkPolicy{}) {
	//	log.Fatalf("can not convert object %#v to calico profile. Object is not of type *netv1.NetworkPolicy", k8sObj)
	//}

	p.k8sPolicy = k8sObj.(*netv1.NetworkPolicy)

	calicoPolicy := api.NewPolicy()

	name := fmt.Sprintf("%s.%s", p.k8sPolicy.ObjectMeta.Namespace, p.k8sPolicy.ObjectMeta.Name)

	metadata := api.PolicyMetadata{
		Name: name,
	}
	
	polOrder := netPolOrder
	selectors, err := p.calculatePodSelectors(p.k8sPolicy.Spec.PodSelector)
	if err != nil {
		return nil, err
	}

	rules, err := p.calculateInboundRules()
	if err != nil {
		return nil, err
	}

	egressRules := make([]api.Rule, 1)
	egressRules = append(egressRules, api.Rule{Action: "allow"})

	calicoPolicy.Metadata = metadata
	calicoPolicy.Spec.Order = &polOrder
	calicoPolicy.Spec.Selector = selectors
	calicoPolicy.Spec.IngressRules = rules
	calicoPolicy.Spec.EgressRules = egressRules
	return *calicoPolicy, nil
}

// calculateSelectors Generates the Calico representation of the policy.spec.podSelector for
// this Policy. Returns the endpoint selector in the Calico datamodel format.
func (p *policyConverter) calculatePodSelectors(podSelector metav1.LabelSelector) (string, error) {
	log.Debugf("Calculating pod selector %#v", podSelector)

	var calicoSelectors []string

	// PodSelectors only select pods from the Policy's namespace.
	// TODO(vinayak): fix k8sNamespacelable undefined
	calicoSelectors = append(calicoSelectors, fmt.Sprintf(calicoSelectorFormat, "k8sNamespaceLabel", p.k8sPolicy.ObjectMeta.Namespace))

	selectors, err := calculateSelectors(podSelector, "%s")
	if err != nil {
		return "", err
	}

	calicoSelectors = append(calicoSelectors, selectors...)
	calicoSelectorsStr := strings.Join(calicoSelectors, " && ")
	return calicoSelectorsStr, nil
}

func calculateSelectors(labelSelector metav1.LabelSelector, keyformat string) ([]string, error) {
	var calicoSelectors []string
	for key, value := range labelSelector.MatchLabels {
		modifiedKey := fmt.Sprintf(keyformat, key)
		calicoSelectors = append(calicoSelectors, fmt.Sprintf(calicoSelectorFormat, modifiedKey, value))
	}

	for _, expression := range labelSelector.MatchExpressions {
		key := fmt.Sprintf(keyformat, expression.Key)
		operator := expression.Operator
		values := expression.Values
		var quotedValues []string
		for _, value := range values {
			quotedValues = append(quotedValues, fmt.Sprintf("\"%s\"", value))
		}
		valuesList := strings.Join(quotedValues, ",")

		switch operator {
		case metav1.LabelSelectorOpIn:
			calicoSelectors = append(calicoSelectors, fmt.Sprintf("%s in { %s }", key, valuesList))
		case metav1.LabelSelectorOpNotIn:
			calicoSelectors = append(calicoSelectors, fmt.Sprintf("%s not in { %s }", key, valuesList))
		case metav1.LabelSelectorOpExists:
			calicoSelectors = append(calicoSelectors, fmt.Sprintf("has(%s)", key))
		case metav1.LabelSelectorOpDoesNotExist:
			calicoSelectors = append(calicoSelectors, fmt.Sprintf("! has(%s)", key))
		default:
			return nil, fmt.Errorf("Unknown operator: %s", operator)
		}
	}
	return calicoSelectors, nil
}

// calculateInboundRules Generates Calico Rule objects for this Policy's ingress rules.
// Returns a list of Calico datamodel Rules.
func (p *policyConverter) calculateInboundRules() ([]api.Rule, error) {
	log.Debug("Calculating inbound rules")

	var rules []api.Rule

	ingressRules := p.k8sPolicy.Spec.Ingress

	if ingressRules != nil && len(ingressRules) > 0 {
		log.Debugf("Got %d ingress rules: translating to Calico format", len(ingressRules))
		for _, ingressRule := range ingressRules {
			log.Debugf("Processing ingress rule %#v", ingressRule)
			if len(ingressRule.Ports) == 0 && len(ingressRule.From) == 0 {

				// An empty rule means allow all traffic.
				log.Debug("Empty rule => allow all; skipping rest")
				rules = append(rules, api.Rule{Action: "allow"})
			} else {

				// Convert ingress rule into Calico Rules.
				log.Debugf("Adding rule %#v", ingressRule)
				incomingRules, err := p.allowIncomingToRules(ingressRule)
				if err != nil {
					return nil, err
				}

				// Merge slices of rules https://golang.org/pkg/builtin/#append
				rules = append(rules, incomingRules...)
			}
		}
	}
	log.Debugf("Calculated total set of rules: %#v", rules)
	return rules, nil
}

// Takes a single "allowIncoming" rule from a NetworkPolicy object
// and returns a list of Calico Rule object with implement it.
func (p *policyConverter) allowIncomingToRules(ingressRule netv1.NetworkPolicyIngressRule) ([]api.Rule, error) {
	log.Debug("Processing ingress rule: %s", ingressRule)

	toArgs := make(map[string]api.EntityRule)
	var err error
	// Generate to "to" arguments for this Rule.
	ports := ingressRule.Ports
	if len(ports) > 0 {
		log.Debug("Parsing 'ports': %s", ports)
		toArgs, err = p.generateToArgs(ports)
		if err != nil {
			return nil, err
		}
	} else {
		log.Debug("No ports specified, allow all protocols / ports")
		toArgs[strings.ToLower(string(v1.ProtocolTCP))] = api.EntityRule{}
	}

	// Generate "from" arguments for this Rule.
	var fromArgs []api.EntityRule
	froms := ingressRule.From
	if len(froms) > 0 {
		log.Debug("Parsing 'from': %s", froms)
		fromArgs, err = p.generateFromArgs(froms)  

		if err != nil {
			return nil, err
		}
	} else {
		log.Debug("No from specified, allow from all sources")
		fromArgs = append(fromArgs, api.EntityRule{})
	}

	// Create a Rule per-protocol, per-from-clause.
	log.Debug("Creating rules")
	var rules []api.Rule
	for _, fromArg := range fromArgs {
		for protocol, toArg := range toArgs {
			log.Debugf("\tAllow from %s to %s over %s", fromArg, toArg, protocol)
			proto := numorstring.ProtocolFromString(protocol)
			rule := api.Rule{
				Action:      "allow",
				Protocol:    &proto,
				Source:      fromArg,
				Destination: toArg,
			}
			rules = append(rules, rule)
		}
	}
	return rules, nil
}

// Generates an arguments dictionary suitable for passing to
// the constructor of a libcalico Rule object from the given ports.
func (p *policyConverter) generateToArgs(ports []netv1.NetworkPolicyPort) (map[string]api.EntityRule, error) {
	// Keep a map of ports exposed, keyed by protocol.
	portsByProtocol := make(map[string][]numorstring.Port)

	// Generate a list of ports allow for each specified protocol.
	for _, toPort := range ports {

		var protocol string
		if toPort.Protocol != nil {
			protocol = strings.ToLower(string(*toPort.Protocol))
		} else {
			protocol = strings.ToLower(string(v1.ProtocolTCP))
		}

		//set, portList := setDefault(portsByProtocol, protocol, make([]numorstring.Port, 0))
		_, portList := setDefault(portsByProtocol, protocol, make([]numorstring.Port, 0))

		// Convert k8s Port in *intstr.IntOrString format to calico Port in numorstring.Port format.
		calicoPort, err := numorstring.PortFromString(toPort.Port.String())
		if err != nil {
			return nil, err
		}
		portList = append(portList, calicoPort)
	}

	toArgs := make(map[string]api.EntityRule)

	for protocol, portList := range portsByProtocol {
		toArgs[protocol] = api.EntityRule{
			Ports: portList,
		}
	}

	return toArgs, nil
}

// setDefault Helper function to set h[key] = value if key is not present
// else return the value.
func setDefault(h map[string][]numorstring.Port, key string, value []numorstring.Port) (set bool, r []numorstring.Port) {
	if r, set = h[key]; !set {
		h[key] = value
		r = value
		set = true
	}
	return
}

// Generate an arguments dictionary suitable for passing to
// the constructor of a libcalico Rule object using the given
// "from" clauses.
func (p *policyConverter) generateFromArgs(froms []netv1.NetworkPolicyPeer) ([]api.EntityRule, error) {
	fromArgs := make([]api.EntityRule, 0)
	for _, fromClause := range froms {

		// We need to check if the key exists, not just if there is
		// a non-null value.  The presence of the key with a null
		// value means "select all".
		log.Debugf("Parsing 'from' clause: %s", fromClause)

		var podsPresent, namespacePresent bool = false, false
		if fromClause.PodSelector != nil {
			podsPresent = true
		}

		if fromClause.NamespaceSelector != nil {
			namespacePresent = true
		}

		log.Debugf("Is 'podSelector:' present? %s", podsPresent)
		log.Debugf("Is 'namespaceSelector:' present? %s", namespacePresent)

		if podsPresent && namespacePresent {

			// This is an error case according to the API.
			msg := "Policy API does not support both 'pods' and 'namespaces' selectors."
			return nil, errors.New(msg)
		} else if podsPresent {

			log.Debugf("Allow from podSelector: %#v", fromClause.PodSelector)
			selectors, err := calculateSelectors(*fromClause.PodSelector, "%s")
			if err != nil {
				return nil, err
			}

			// We can only select on pods in this namespace.
			// TODO: Fix namespace label
			selectors = append(selectors, fmt.Sprintf(calicoSelectorFormat, "k8sNamespaceLabel", p.k8sPolicy.ObjectMeta.Namespace))
			calicoSelectorsStr := strings.Join(selectors, " && ")

			// Append the selector to the from args.
			log.Debugf("Allowing pods which match: %s", calicoSelectorsStr)
			fromArgs = append(fromArgs, api.EntityRule{Selector: calicoSelectorsStr})
		} else if namespacePresent {

			// Namespace labels are applied to each pod in the namespace using
			// the per-namespace profile.  We can select on namespace labels
			// using the "namespaceLabelKeyFormat" modifier.
			log.Debugf("Allow from namespaceSelector: %#v", fromClause.NamespaceSelector)
			selectors, err := calculateSelectors(*fromClause.NamespaceSelector, namespaceLabelKeyFormat)
			if err != nil {
				return nil, err
			}
			calicoSelectorsStr := strings.Join(selectors, " && ")

			if len(selectors) == 0 {

				// Allow from all pods in all namespaces.
				log.Debug("Allowing from all pods in all namespaces")
				// TODO: Fix namespace label
				selector := fmt.Sprintf("has(%s)", "k8sNamespaceLabel")
				fromArgs = append(fromArgs, api.EntityRule{Selector: selector})
			} else {

				// Allow from the selected namespaces.
				log.Debugf("Allowing from namespaces which match: %s", calicoSelectorsStr)
				fromArgs = append(fromArgs, api.EntityRule{Selector: calicoSelectorsStr})
			}
		}
	}
	return fromArgs, nil
}

// GetKey returns name of network policy as key
func (p *policyConverter) GetKey(obj interface{}) string {

	//if reflect.TypeOf(obj) != reflect.TypeOf(netv1.NetworkPolicy{}) {
	//	log.Fatalf("can not construct key for object %#v. Object is not of type netv1.NetworkPolicy", obj)
	//}
	policy := obj.(api.Policy)
	return policy.Metadata.Name
}

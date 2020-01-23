package kubernetes

import (
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/restmapper"
)

func resourceKubernetesCustom() *schema.Resource {
	return &schema.Resource{
		Create: resourceKubernetesCustomCreate,
		Read:   resourceKubernetesCustomRead,
		Update: resourceKubernetesCustomUpdate,
		Delete: resourceKubernetesCustomDelete,
		// Exists: resourceKubernetesCustomExists,
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},
		Timeouts: &schema.ResourceTimeout{
			Create: schema.DefaultTimeout(5 * time.Minute),
			Delete: schema.DefaultTimeout(5 * time.Minute),
		},

		Schema: map[string]*schema.Schema{
			"json": {
				Type:        schema.TypeString,
				Description: "",
				Required:    true,

				DiffSuppressFunc: func(k, oldJSON, newJSON string, d *schema.ResourceData) bool {
					// FIXME handle errors
					old, _ := parseKubernetesConfig(oldJSON)
					new, _ := parseKubernetesConfig(newJSON)

					if reflect.DeepEqual(old, new) {
						return true
					}

					return false
				},
			},
		},
	}
}

func resourceKubernetesCustomCreate(d *schema.ResourceData, m interface{}) error {
	config := d.Get("json").(string)
	unstructuredResource, _ := parseKubernetesConfig(config)

	clientset := m.(*KubeClientsets).MainClientset
	dclient := m.(*KubeClientsets).DynamicClient
	resource, namespaced, err := createResourceFromUnstructured(unstructuredResource, clientset, dclient)

	if err != nil {
		return fmt.Errorf("Could not determine resource type: %v", err)
	}

	name := unstructuredResource.GetName()
	id := name

	// if strings.ToLower(kind) == "customresourcedefinition" {
	// 	_, err = resource.Create(unstructuredResource, metav1.CreateOptions{})
	// 	id = name
	// } else {
	// 	namespace := getNamespaceOrDefault(unstructuredResource)
	// 	_, err = resource.Namespace(namespace).Create(unstructuredResource, metav1.CreateOptions{})
	// 	id = fmt.Sprintf("%s/%s", namespace, name)
	// }

	var r dynamic.ResourceInterface

	if namespaced {
		log.Printf("[DEBUG] This is a namespaced resource")
		namespace := getNamespaceOrDefault(unstructuredResource)
		r = resource.Namespace(namespace)
		id = fmt.Sprintf("%s/%s", namespace, name)
	} else {
		r = resource
	}

	_, err = r.Create(unstructuredResource, metav1.CreateOptions{})

	if err != nil {
		return fmt.Errorf("Could not create resource: %v", err)
	}

	d.SetId(id)

	return resourceKubernetesCustomRead(d, m)
}

func createResourceFromUnstructured(r *unstructured.Unstructured, clientset *kubernetes.Clientset, dclient dynamic.Interface) (dynamic.NamespaceableResourceInterface, bool, error) {
	// figure out the REST mapping for the resource
	d := clientset.Discovery()
	groupResources, err := restmapper.GetAPIGroupResources(d)

	if err != nil {
		return nil, false, err
	}

	gvk := r.GroupVersionKind()
	gk := gvk.GroupKind()

	rm := restmapper.NewDiscoveryRESTMapper(groupResources)
	mapping, err := rm.RESTMapping(gk, gvk.Version)

	// figure out if the Resource is namespaced
	gv := r.GroupVersionKind().GroupVersion()
	apiResources, err := d.ServerResourcesForGroupVersion(gv.String())

	if err != nil {
		// TODO wrap this in a more meaningful error message
		return nil, false, err
	}

	var namespaced bool
	for _, rl := range apiResources.APIResources {
		if rl.Kind == gk.Kind {
			namespaced = rl.Namespaced
			break
		}
	}

	if err != nil {
		return nil, false, err
	}

	return dclient.Resource(mapping.Resource), namespaced, nil
}

func resourceKubernetesCustomRead(d *schema.ResourceData, m interface{}) error {
	config := d.Get("json").(string)
	unstructuredResource, _ := parseKubernetesConfig(config)

	clientset := m.(*KubeClientsets).MainClientset
	dclient := m.(*KubeClientsets).DynamicClient

	resource, namespaced, _ := createResourceFromUnstructured(unstructuredResource, clientset, dclient)
	name := unstructuredResource.GetName()

	var r dynamic.ResourceInterface

	if namespaced {
		namespace := getNamespaceOrDefault(unstructuredResource)
		r = resource.Namespace(namespace)
	} else {
		r = resource
	}

	res, err := r.Get(name, metav1.GetOptions{})

	if err != nil {
		return fmt.Errorf("Could not get resource: %v", err)
	}

	removeIgnoredFields(res)

	_, namespaceSet, _ := unstructured.NestedString(unstructuredResource.Object, "metadata", "namespace")

	if !namespaceSet {
		unstructured.RemoveNestedField(res.Object, "metadata", "namespace")
	}

	rawJSON, err := res.MarshalJSON()

	d.Set("json", string(rawJSON))

	return nil
}

func resourceKubernetesCustomUpdate(d *schema.ResourceData, m interface{}) error {
	if d.HasChange("json") {
		config := d.Get("json").(string)
		unstructuredResource, _ := parseKubernetesConfig(config)

		clientset := m.(*KubeClientsets).MainClientset
		dclient := m.(*KubeClientsets).DynamicClient
		resource, namespaced, _ := createResourceFromUnstructured(unstructuredResource, clientset, dclient)
		name := unstructuredResource.GetName()

		var r dynamic.ResourceInterface

		if namespaced {
			namespace := getNamespaceOrDefault(unstructuredResource)
			r = resource.Namespace(namespace)
		} else {
			r = resource
		}

		res, err := r.Get(name, metav1.GetOptions{})

		resourceVersion := res.GetResourceVersion()
		unstructuredResource.SetResourceVersion(resourceVersion)

		_, err = r.Update(unstructuredResource, metav1.UpdateOptions{})

		if err != nil {
			return fmt.Errorf("Could not update resource: %v", err)
		}
	}

	return resourceKubernetesCustomRead(d, m)
}

func resourceKubernetesCustomDelete(d *schema.ResourceData, m interface{}) error {
	config := d.Get("json").(string)
	unstructuredResource, _ := parseKubernetesConfig(config)

	clientset := m.(*KubeClientsets).MainClientset
	dclient := m.(*KubeClientsets).DynamicClient

	resource, namespaced, _ := createResourceFromUnstructured(unstructuredResource, clientset, dclient)
	name := unstructuredResource.GetName()

	var r dynamic.ResourceInterface

	if namespaced {
		namespace := getNamespaceOrDefault(unstructuredResource)
		r = resource.Namespace(namespace)
	} else {
		r = resource
	}

	err := r.Delete(name, &metav1.DeleteOptions{})

	if err != nil {
		return fmt.Errorf("Could not delete resource: %v", err)
	}

	return nil
}

func getNamespaceOrDefault(u *unstructured.Unstructured) string {
	n := u.GetNamespace()

	if n == "" {
		return "default"
	}

	return n
}

func resourceKubernetesCustomExists(d *schema.ResourceData, m interface{}) (bool, error) {

	return false, nil
}

var ignoredFields = [][]string{
	[]string{"metadata", "creationTimestamp"},
	[]string{"metadata", "resourceVersion"},
	[]string{"metadata", "uid"},
	[]string{"metadata", "selfLink"},
	[]string{"metadata", "generation"},
	[]string{"status"},
}

func removeIgnoredFields(u *unstructured.Unstructured) {
	for _, field := range ignoredFields {
		unstructured.RemoveNestedField(u.Object, field...)
	}
}

// parseKubernetesConfig will parse a JSON string into an Unstructured
func parseKubernetesConfig(config string) (*unstructured.Unstructured, error) {
	var m map[string]interface{}

	err := json.Unmarshal([]byte(config), &m)

	if err != nil {
		return nil, err
	}

	var u = unstructured.Unstructured{
		Object: m,
	}

	removeIgnoredFields(&u)

	return &u, nil
}

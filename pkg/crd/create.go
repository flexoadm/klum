package crd

import (
	"context"
	"time"

	"github.com/rancher/wrangler/v3/pkg/crd"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

func Create(ctx context.Context, config *rest.Config) error {
	client, err := clientset.NewForConfig(config)
	if err != nil {
		return err
	}
	return crd.BatchCreateCRDs(ctx, client.ApiextensionsV1().CustomResourceDefinitions(), nil, 60*time.Second, Definitions())
}

// Definitions returns fresh structural schemas for the cluster-scoped klum API.
func Definitions() []*apiextv1.CustomResourceDefinition {
	str := apiextv1.JSONSchemaProps{Type: "string"}
	user := object(map[string]apiextv1.JSONSchemaProps{
		"enabled":      {Type: "boolean"},
		"clusterRoles": array(str),
		"roles":        array(object(map[string]apiextv1.JSONSchemaProps{"namespace": str, "clusterRole": str, "role": str})),
	})
	conditions := array(object(map[string]apiextv1.JSONSchemaProps{
		"type": str, "status": str, "reason": str, "message": str,
		"lastUpdateTime": str, "lastTransitionTime": str,
	}))
	kubeconfig := object(map[string]apiextv1.JSONSchemaProps{
		"clusters": array(object(map[string]apiextv1.JSONSchemaProps{
			"name": str, "cluster": object(map[string]apiextv1.JSONSchemaProps{"server": str, "certificate-authority-data": str}),
		})),
		"users": array(object(map[string]apiextv1.JSONSchemaProps{
			"name": str, "user": object(map[string]apiextv1.JSONSchemaProps{"token": str}),
		})),
		"contexts": array(object(map[string]apiextv1.JSONSchemaProps{
			"name": str, "context": object(map[string]apiextv1.JSONSchemaProps{"cluster": str, "user": str}),
		})),
		"current-context": str,
	})
	return []*apiextv1.CustomResourceDefinition{
		definition("User", "users", "user", user, object(map[string]apiextv1.JSONSchemaProps{"conditions": conditions})),
		definition("Kubeconfig", "kubeconfigs", "kubeconfig", kubeconfig, apiextv1.JSONSchemaProps{}),
	}
}

func definition(kind, plural, singular string, spec, status apiextv1.JSONSchemaProps) *apiextv1.CustomResourceDefinition {
	properties := map[string]apiextv1.JSONSchemaProps{
		"apiVersion": {Type: "string"}, "kind": {Type: "string"}, "metadata": {Type: "object"}, "spec": spec,
	}
	version := apiextv1.CustomResourceDefinitionVersion{Name: "v1alpha1", Served: true, Storage: true}
	if status.Type != "" {
		properties["status"] = status
		version.Subresources = &apiextv1.CustomResourceSubresources{Status: &apiextv1.CustomResourceSubresourceStatus{}}
	}
	root := object(properties)
	version.Schema = &apiextv1.CustomResourceValidation{OpenAPIV3Schema: &root}
	return &apiextv1.CustomResourceDefinition{
		TypeMeta:   metav1.TypeMeta{APIVersion: "apiextensions.k8s.io/v1", Kind: "CustomResourceDefinition"},
		ObjectMeta: metav1.ObjectMeta{Name: plural + ".klum.cattle.io"},
		Spec: apiextv1.CustomResourceDefinitionSpec{
			Group: "klum.cattle.io", Scope: apiextv1.ClusterScoped,
			Names:    apiextv1.CustomResourceDefinitionNames{Kind: kind, ListKind: kind + "List", Plural: plural, Singular: singular},
			Versions: []apiextv1.CustomResourceDefinitionVersion{version},
		},
	}
}

func object(properties map[string]apiextv1.JSONSchemaProps) apiextv1.JSONSchemaProps {
	return apiextv1.JSONSchemaProps{Type: "object", Properties: properties}
}

func array(items apiextv1.JSONSchemaProps) apiextv1.JSONSchemaProps {
	return apiextv1.JSONSchemaProps{Type: "array", Items: &apiextv1.JSONSchemaPropsOrArray{Schema: &items}}
}

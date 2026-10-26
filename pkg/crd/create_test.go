package crd

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	klum "github.com/ibuildthecloud/klum/pkg/apis/klum.cattle.io/v1alpha1"
	"github.com/rancher/wrangler/v3/pkg/genericcondition"
	corev1 "k8s.io/api/core/v1"
	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/pruning"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDefinitionsValidateAndPreserveAPIPayloads(t *testing.T) {
	enabled := true
	fixtures := []interface{}{
		&klum.User{
			TypeMeta:   metav1.TypeMeta{APIVersion: "klum.cattle.io/v1alpha1", Kind: "User"},
			ObjectMeta: metav1.ObjectMeta{Name: "alice"},
			Spec:       klum.UserSpec{Enabled: &enabled, ClusterRoles: []string{"view"}, Roles: []klum.NamespaceRole{{Namespace: "team", Role: "reader", ClusterRole: "view"}}},
			Status:     klum.UserStatus{Conditions: []genericcondition.GenericCondition{{Type: "Ready", Status: corev1.ConditionTrue, Reason: "Applied", Message: "ready", LastUpdateTime: "2026-01-01T00:00:00Z", LastTransitionTime: "2026-01-01T00:00:00Z"}}},
		},
		&klum.Kubeconfig{
			TypeMeta:   metav1.TypeMeta{APIVersion: "klum.cattle.io/v1alpha1", Kind: "Kubeconfig"},
			ObjectMeta: metav1.ObjectMeta{Name: "alice"},
			Spec: klum.KubeconfigSpec{
				Clusters:  []klum.NamedCluster{{Name: "test", Cluster: klum.Cluster{Server: "https://example.test", CertificateAuthorityData: "Y2E="}}},
				AuthInfos: []klum.NamedAuthInfo{{Name: "test", AuthInfo: klum.AuthInfo{Token: "test-token"}}},
				Contexts:  []klum.NamedContext{{Name: "test", Context: klum.Context{Cluster: "test", AuthInfo: "test"}}}, CurrentContext: "test",
			},
		},
	}
	for i, def := range Definitions() {
		t.Run(def.Name, func(t *testing.T) {
			var internal apiext.CustomResourceDefinition
			if err := apiextv1.Convert_v1_CustomResourceDefinition_To_apiextensions_CustomResourceDefinition(def, &internal, nil); err != nil {
				t.Fatal(err)
			}
			// The API server fills accepted names and stored versions on creation.
			internal.Status.StoredVersions = []string{"v1alpha1"}
			if errs := validation.ValidateCustomResourceDefinition(context.Background(), &internal); len(errs) != 0 {
				t.Fatalf("invalid CRD: %v", errs)
			}
			var internalSchema apiext.JSONSchemaProps
			if err := apiextv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(def.Spec.Versions[0].Schema.OpenAPIV3Schema, &internalSchema, nil); err != nil {
				t.Fatal(err)
			}
			schema, err := structuralschema.NewStructural(&internalSchema)
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(fixtures[i])
			if err != nil {
				t.Fatal(err)
			}
			var before, after map[string]interface{}
			if err := json.Unmarshal(data, &before); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &after); err != nil {
				t.Fatal(err)
			}
			after["unknownField"] = "must be pruned"
			pruning.Prune(after, schema, true)
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("schema pruned supported fields:\nbefore: %v\nafter: %v", before, after)
			}
		})
	}
}

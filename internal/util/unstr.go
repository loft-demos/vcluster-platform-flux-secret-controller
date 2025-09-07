package util

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

// GetString returns a nested string or empty if not present.
func GetString(u *unstructured.Unstructured, fields ...string) string {
	v, _, _ := unstructured.NestedString(u.Object, fields...)
	return v
}

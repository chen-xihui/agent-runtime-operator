package controllers

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/diff"
)

func TestBuildDefaultDenyNetworkPolicy(t *testing.T) {
	np := buildDefaultDenyNetworkPolicy("tenant-a")

	if np.Name != "tenant-default-deny" {
		t.Fatalf("name = %q", np.Name)
	}
	if np.Namespace != "tenant-a" {
		t.Fatalf("namespace = %q", np.Namespace)
	}
	// 默认 Deny-All：Ingress + Egress，PodSelector 全选
	if len(np.Spec.PolicyTypes) != 2 {
		t.Fatalf("policyTypes = %v, want [Ingress Egress]", np.Spec.PolicyTypes)
	}
	// PodSelector 应为空（匹配所有 Pod）
	if !equality.Semantic.DeepEqual(np.Spec.PodSelector, metav1.LabelSelector{}) {
		t.Fatalf("podSelector = %v, want empty (all pods)", diff.ObjectReflectDiff(np.Spec.PodSelector, metav1.LabelSelector{}))
	}
}

func TestWantSuspend(t *testing.T) {
	if wantSuspend(nil) {
		t.Fatal("nil suspend should be false")
	}
	if wantSuspend(boolPtr(false)) {
		t.Fatal("false suspend should be false")
	}
	if !wantSuspend(boolPtr(true)) {
		t.Fatal("true suspend should be true")
	}
}

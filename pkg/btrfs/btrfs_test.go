package btrfs

import (
	"reflect"
	"testing"
)

// TestManagerInterface verifies that the Manager interface exists with all required methods.
func TestManagerInterface(t *testing.T) {
	// Get the Manager interface type
	managerType := reflect.TypeOf((*Manager)(nil)).Elem()

	// Expected methods
	expectedMethods := []string{
		"CreateSubvolume",
		"DeleteSubvolume",
		"SubvolumeExists",
		"CreateSnapshot",
		"EnsureQuotaEnabled",
		"SetQgroupLimit",
		"RemoveQgroupLimit",
		"ClearStaleQgroups",
		"GetQgroupUsage",
		"GetFilesystemUsage",
		"IsBtrfsFilesystem",
	}

	// Verify all expected methods exist
	for _, methodName := range expectedMethods {
		method, found := managerType.MethodByName(methodName)
		if !found {
			t.Errorf("Manager interface missing method: %s", methodName)
			continue
		}
		t.Logf("Found method: %s with signature: %v", methodName, method.Type)
	}

	// Verify method count matches
	if managerType.NumMethod() != len(expectedMethods) {
		t.Errorf("Manager interface has %d methods, expected %d", managerType.NumMethod(), len(expectedMethods))
	}
}

// TestQgroupUsageType verifies that QgroupUsage struct exists with required fields.
func TestQgroupUsageType(t *testing.T) {
	qgroupType := reflect.TypeOf(QgroupUsage{})

	// Check for required fields
	requiredFields := []string{"Referenced", "Exclusive", "MaxRfer"}
	for _, fieldName := range requiredFields {
		field, found := qgroupType.FieldByName(fieldName)
		if !found {
			t.Errorf("QgroupUsage struct missing field: %s", fieldName)
			continue
		}
		// Verify field is uint64
		if field.Type.Kind() != reflect.Uint64 {
			t.Errorf("QgroupUsage.%s should be uint64, got %v", fieldName, field.Type.Kind())
		}
	}

	// Verify field count
	if qgroupType.NumField() != len(requiredFields) {
		t.Errorf("QgroupUsage has %d fields, expected %d", qgroupType.NumField(), len(requiredFields))
	}
}

// TestFsUsageType verifies that FsUsage struct exists with required fields.
func TestFsUsageType(t *testing.T) {
	fsUsageType := reflect.TypeOf(FsUsage{})

	// Check for required fields
	requiredFields := []string{"Total", "Used", "Available"}
	for _, fieldName := range requiredFields {
		field, found := fsUsageType.FieldByName(fieldName)
		if !found {
			t.Errorf("FsUsage struct missing field: %s", fieldName)
			continue
		}
		// Verify field is uint64
		if field.Type.Kind() != reflect.Uint64 {
			t.Errorf("FsUsage.%s should be uint64, got %v", fieldName, field.Type.Kind())
		}
	}

	// Verify field count
	if fsUsageType.NumField() != len(requiredFields) {
		t.Errorf("FsUsage has %d fields, expected %d", fsUsageType.NumField(), len(requiredFields))
	}
}

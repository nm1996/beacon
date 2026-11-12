package auth

import (
	"strings"
	"testing"
)

func TestManageMustBeGrantedExplicitly(test *testing.T) {
	path := writeTokens(test, "[[tokens]]\nname = \"service\"\nsha256 = \""+mustHash(test, ordersToken)+"\"\n"+
		"[[tokens]]\nname = \"operator\"\nsha256 = \""+mustHash(test, scraperToken)+"\"\npermissions = [\"register\", \"manage\"]\n")

	identities, err := Load(path)
	if err != nil {
		test.Fatalf("load: %v", err)
	}

	service, _ := identities.Authenticate(ordersToken)
	if service.Allows(PermissionManage) {
		test.Error("expected an entry without a permission list not to hold manage")
	}

	for _, permission := range []Permission{PermissionDiscover, PermissionRegister, PermissionMetrics} {
		if !service.Allows(permission) {
			test.Errorf("expected an entry without a permission list to hold %s", permission)
		}
	}

	operator, _ := identities.Authenticate(scraperToken)
	if !operator.Allows(PermissionManage) || !operator.Allows(PermissionRegister) {
		test.Errorf("expected the operator to hold register and manage, got %+v", operator)
	}
}

func TestManageWithoutRegisterIsRejected(test *testing.T) {
	for _, entry := range []string{
		"[[tokens]]\nname = \"a\"\nsha256 = \"" + mustHash(test, ordersToken) + "\"\npermissions = [\"manage\"]",
		"[[certificates]]\nname = \"a\"\ndns = \"a.internal\"\npermissions = [\"discover\", \"manage\"]",
	} {
		_, err := Load(writeTokens(test, entry))

		if err == nil || !strings.Contains(err.Error(), "manage only works together with register") {
			test.Errorf("expected manage without register to be rejected, got %v", err)
		}
	}
}

package governance_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestArchitecturalPurity enforces the Golden Rule of the Pure Financial Accounting Engine:
// The engine must be strictly financial (General Ledger / Subledgers / Treasury / Reporting / Masterdata).
// It must NOT contain operational or commercial entities (e.g., SKU, Product, Warehouse, Shipment,
// Order, Cart, EmployeeDepartment, CustomerName, VendorName).
func TestArchitecturalPurity(t *testing.T) {
	// Root of the accounting engine repository
	root := filepath.Join("..", "..")

	// Prohibited patterns in struct names, type names, interface names, and field names.
	bannedIdentPatterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bSKU\b`),
		regexp.MustCompile(`(?i)\bProduct\b`),
		regexp.MustCompile(`(?i)\bWarehouse\b`),
		regexp.MustCompile(`(?i)\bShipment\b`),
		regexp.MustCompile(`(?i)\bCart\b`),
		regexp.MustCompile(`(?i)\bShoppingCart\b`),
		regexp.MustCompile(`(?i)\bSalesOrder\b`),
		regexp.MustCompile(`(?i)\bPurchaseOrder\b`),
		regexp.MustCompile(`(?i)\bCustomerOrder\b`),
		regexp.MustCompile(`(?i)\bOrderItem\b`),
		regexp.MustCompile(`(?i)\bWorkOrder\b`),
		regexp.MustCompile(`(?i)\bEmployeeDepartment\b`),
		regexp.MustCompile(`(?i)\bCustomerName\b`),
		regexp.MustCompile(`(?i)\bVendorName\b`),
		regexp.MustCompile(`(?i)\bInventoryItem\b`),
		regexp.MustCompile(`(?i)\bDeliveryNote\b`),
		regexp.MustCompile(`(?i)\bBillOfLading\b`),
	}

	// Exact banned type names (must not be defined anywhere in production packages)
	bannedTypeNames := map[string]bool{
		"Product":            true,
		"SKU":                true,
		"Warehouse":          true,
		"Shipment":           true,
		"Order":              true,
		"Cart":               true,
		"Customer":           true,
		"Vendor":             true,
		"Client":             true,
		"Supplier":           true,
		"EmployeeDepartment": true,
		"Inventory":          true,
	}

	var violations []string

	fset := token.NewFileSet()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			name := info.Name()
			if name == ".git" || name == "vendor" || name == "docs" {
				return filepath.SkipDir
			}
			return nil
		}
		// Only check Go files
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Skip purity test itself to avoid self-flagging patterns
		if strings.HasSuffix(path, "purity_test.go") {
			return nil
		}

		node, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}

		ast.Inspect(node, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.TypeSpec:
				typeName := x.Name.Name
				if bannedTypeNames[typeName] {
					violations = append(violations, fset.Position(x.Pos()).String()+": banned type definition: "+typeName)
				}
				for _, re := range bannedIdentPatterns {
					if re.MatchString(typeName) {
						violations = append(violations, fset.Position(x.Pos()).String()+": type name matches banned pattern: "+typeName)
					}
				}

				// If it's a struct, check field names
				if st, ok := x.Type.(*ast.StructType); ok {
					for _, field := range st.Fields.List {
						for _, fieldIdent := range field.Names {
							fieldName := fieldIdent.Name
							for _, re := range bannedIdentPatterns {
								if re.MatchString(fieldName) {
									violations = append(violations, fset.Position(fieldIdent.Pos()).String()+": struct "+typeName+" has banned field: "+fieldName)
								}
							}
						}
					}
				}

			case *ast.FuncDecl:
				funcName := x.Name.Name
				for _, re := range bannedIdentPatterns {
					if re.MatchString(funcName) {
						violations = append(violations, fset.Position(x.Pos()).String()+": function matches banned pattern: "+funcName)
					}
				}
			}
			return true
		})

		return nil
	})

	if err != nil {
		t.Fatalf("failed to walk codebase: %v", err)
	}

	if len(violations) > 0 {
		t.Errorf("Architectural Purity Violations (Golden Rule violated: operational/commercial entities detected):\n%s",
			strings.Join(violations, "\n"))
	}
}

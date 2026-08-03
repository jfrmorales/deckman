package deck

import "testing"

// Los nombres esperados salen del config.vdf de una Deck real, donde Steam
// tenia escritos proton_42, proton_8, proton_9 y proton_experimental.
func TestProtonInternalName(t *testing.T) {
	cases := map[string]string{
		"Proton Experimental":              "proton_experimental",
		"Proton Hotfix":                    "proton_hotfix",
		"Proton 4.2":                       "proton_42",
		"Proton 6.3":                       "proton_63",
		"Proton 7.0":                       "proton_7",
		"Proton 8.0":                       "proton_8",
		"Proton 9.0":                       "proton_9",
		"Proton 9.0 (Beta)":                "proton_9",
		"Proton 10.0":                      "proton_10",
		"Proton 11.0":                      "proton_11",
		"Proton 3.16":                      "proton_316",
		"Proton 5.13":                      "proton_513",
		"Proton EasyAntiCheat Runtime":     "",
		"Steam Linux Runtime 3.0 (sniper)": "",
		"Rocket League":                    "",
		"":                                 "",
	}
	for in, want := range cases {
		if got := protonInternalName(in); got != want {
			t.Errorf("protonInternalName(%q) = %q, se esperaba %q", in, got, want)
		}
	}
}

func TestMergeToolsDedupes(t *testing.T) {
	out := mergeTools(
		[]CompatTool{{Name: "proton_9", DisplayName: "Proton 9.0"}, {Name: "proton_8", DisplayName: "Proton 8.0"}},
		[]CompatTool{{Name: "proton_9", DisplayName: "duplicado"}, {Name: "GE-Proton9-22", DisplayName: "GE-Proton9-22"}},
	)
	if len(out) != 3 {
		t.Fatalf("se esperaban 3 herramientas, salieron %d: %+v", len(out), out)
	}
	// Los oficiales van primero.
	if out[0].Name != "proton_8" {
		t.Errorf("el primero deberia ser un Proton oficial, es %s", out[0].Name)
	}
	if out[2].Name != "GE-Proton9-22" {
		t.Errorf("el custom deberia ir al final, esta %s", out[2].Name)
	}
}

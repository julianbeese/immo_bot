package config

import (
	"os"
	"path/filepath"
	"testing"
)

func testCfg() *Config {
	return &Config{
		Message:         MessageConfig{TemplatePath: "global.txt"},
		Contact:         ContactConfig{Profile: ContactProfile{FirstName: "Global", Email: "g@example.com"}},
		DefaultCampaign: "single",
		Campaigns: map[string]Campaign{
			"single": {MessageTemplatePath: "single.txt", AIPrompt: "S"},
			"wg":     {MessageTemplatePath: "wg.txt", AIPrompt: "W", Contact: ContactProfile{FirstName: "Jay", Email: "j@example.com"}},
		},
	}
}

func TestResolveCampaignExplicit(t *testing.T) {
	c := testCfg()
	wg := c.ResolveCampaign("wg")
	if wg.MessageTemplatePath != "wg.txt" || wg.AIPrompt != "W" || wg.Contact.FirstName != "Jay" {
		t.Errorf("wg campaign wrong: %+v", wg)
	}
}

func TestResolveCampaignFillsContactFromGlobal(t *testing.T) {
	c := testCfg()
	// "single" has no contact_profile → should inherit the global one.
	single := c.ResolveCampaign("single")
	if single.Contact.FirstName != "Global" {
		t.Errorf("single should inherit global contact, got %q", single.Contact.FirstName)
	}
	if single.MessageTemplatePath != "single.txt" {
		t.Errorf("single template = %q", single.MessageTemplatePath)
	}
}

func TestResolveCampaignFallsBackToDefault(t *testing.T) {
	c := testCfg()
	got := c.ResolveCampaign("does-not-exist")
	if got.MessageTemplatePath != "single.txt" {
		t.Errorf("unknown category should use default campaign, got %q", got.MessageTemplatePath)
	}
	if c.ResolveCampaign("").MessageTemplatePath != "single.txt" {
		t.Error("empty category should use default campaign")
	}
}

func TestResolveCampaignGlobalFallback(t *testing.T) {
	c := &Config{Message: MessageConfig{TemplatePath: "global.txt"}}
	got := c.ResolveCampaign("anything")
	if got.MessageTemplatePath != "global.txt" {
		t.Errorf("with no campaigns, should fall back to global template, got %q", got.MessageTemplatePath)
	}
}

func TestLoadSynthesizesDefaultCampaign(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("message:\n  template_path: my.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultCampaign != "default" {
		t.Errorf("DefaultCampaign = %q, want default", cfg.DefaultCampaign)
	}
	if _, ok := cfg.Campaigns["default"]; !ok {
		t.Error("a default campaign should be synthesized when none configured")
	}
	if !cfg.HasCampaign("default") {
		t.Error("HasCampaign(default) should be true")
	}
}

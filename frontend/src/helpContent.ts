// Route -> HelpRail content for the 8 CRUD list pages (per
// docs/superpowers/specs/2026-07-13-console-help-rail-design.md).
// Dashboard/Audit/Usage/Billing/Settings are chart/tab layouts and
// deliberately have no entry here — App.tsx renders no rail for them.
import type { IconName } from "./components/ui";

export interface HelpTipKeys {
  titleKey: string;
  bodyKey: string;
}

export const HELP_CONTENT: Record<string, { icon: IconName; tips: HelpTipKeys[] }> = {
  "/keys": {
    icon: "key",
    tips: [
      { titleKey: "help_keys_1_title", bodyKey: "help_keys_1_body" },
      { titleKey: "help_keys_2_title", bodyKey: "help_keys_2_body" },
      { titleKey: "help_keys_3_title", bodyKey: "help_keys_3_body" },
    ],
  },
  "/providers": {
    icon: "providers",
    tips: [
      { titleKey: "help_providers_1_title", bodyKey: "help_providers_1_body" },
      { titleKey: "help_providers_2_title", bodyKey: "help_providers_2_body" },
      { titleKey: "help_providers_3_title", bodyKey: "help_providers_3_body" },
    ],
  },
  "/models-pricing": {
    icon: "pricetag",
    tips: [
      { titleKey: "help_modelsPricing_1_title", bodyKey: "help_modelsPricing_1_body" },
      { titleKey: "help_modelsPricing_2_title", bodyKey: "help_modelsPricing_2_body" },
      { titleKey: "help_modelsPricing_3_title", bodyKey: "help_modelsPricing_3_body" },
    ],
  },
  "/model-mappings": {
    icon: "sync",
    tips: [
      { titleKey: "help_modelMappings_1_title", bodyKey: "help_modelMappings_1_body" },
      { titleKey: "help_modelMappings_2_title", bodyKey: "help_modelMappings_2_body" },
      { titleKey: "help_modelMappings_3_title", bodyKey: "help_modelMappings_3_body" },
    ],
  },
  "/guardrail-policies": {
    icon: "alert",
    tips: [
      { titleKey: "help_guardrailPolicies_1_title", bodyKey: "help_guardrailPolicies_1_body" },
      { titleKey: "help_guardrailPolicies_2_title", bodyKey: "help_guardrailPolicies_2_body" },
      { titleKey: "help_guardrailPolicies_3_title", bodyKey: "help_guardrailPolicies_3_body" },
    ],
  },
  "/mcp-servers": {
    icon: "providers",
    tips: [
      { titleKey: "help_mcpServers_1_title", bodyKey: "help_mcpServers_1_body" },
      { titleKey: "help_mcpServers_2_title", bodyKey: "help_mcpServers_2_body" },
      { titleKey: "help_mcpServers_3_title", bodyKey: "help_mcpServers_3_body" },
    ],
  },
  "/tenants": {
    icon: "tenants",
    tips: [
      { titleKey: "help_tenants_1_title", bodyKey: "help_tenants_1_body" },
      { titleKey: "help_tenants_2_title", bodyKey: "help_tenants_2_body" },
      { titleKey: "help_tenants_3_title", bodyKey: "help_tenants_3_body" },
    ],
  },
  "/users": {
    icon: "users",
    tips: [
      { titleKey: "help_usersAccess_1_title", bodyKey: "help_usersAccess_1_body" },
      { titleKey: "help_usersAccess_2_title", bodyKey: "help_usersAccess_2_body" },
      { titleKey: "help_usersAccess_3_title", bodyKey: "help_usersAccess_3_body" },
    ],
  },
};

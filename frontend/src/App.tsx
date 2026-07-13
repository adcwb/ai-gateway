import { useEffect, useState } from "react";
import { Navigate, NavLink, Route, Routes, useLocation, useNavigate } from "react-router-dom";
import { api, clearToken, getToken } from "./api/client";
import { getLang, setLang, t, type Lang } from "./i18n";
import { BrandMark, HelpRail, Icon, type IconName } from "./components/ui";
import { HELP_CONTENT } from "./helpContent";
import Login from "./pages/Login";
import Dashboard from "./pages/Dashboard";
import Keys from "./pages/Keys";
import Providers from "./pages/Providers";
import McpServers from "./pages/McpServers";
import ModelsPricing from "./pages/ModelsPricing";
import ModelMappings from "./pages/ModelMappings";
import GuardrailPolicies from "./pages/GuardrailPolicies";
import Audit from "./pages/Audit";
import Tenants from "./pages/Tenants";
import Billing from "./pages/Billing";
import Settings from "./pages/Settings";
import Users from "./pages/Users";
import Usage from "./pages/Usage";

interface NavItem { to: string; key: string; icon: IconName; end?: boolean }

// Grouped so the eye lands on operate → manage → observe.
// slice bounds below assume: [0,3) operate, [3,11) manage, [11,) observe.
const NAV: NavItem[] = [
  { to: "/", key: "dashboard", icon: "dashboard", end: true },
  { to: "/keys", key: "keys", icon: "key" },
  { to: "/providers", key: "providers", icon: "providers" },
  { to: "/mcp-servers", key: "mcpServers", icon: "providers" },
  { to: "/models-pricing", key: "modelsPricing", icon: "pricetag" },
  { to: "/model-mappings", key: "modelMappings", icon: "sync" },
  { to: "/guardrail-policies", key: "guardrailPolicies", icon: "alert" },
  { to: "/tenants", key: "tenants", icon: "tenants" },
  { to: "/billing", key: "billing", icon: "billing" },
  { to: "/users", key: "usersAccess", icon: "users" },
  { to: "/settings", key: "settings", icon: "settings" },
  { to: "/audit", key: "audit", icon: "audit" },
  { to: "/usage", key: "usage", icon: "dashboard" },
];

const HELP_RAIL_COLLAPSED_KEY = "aigw_help_rail_collapsed";

export default function App() {
  const [lang, setLangState] = useState<Lang>(getLang());
  const [authed, setAuthed] = useState<boolean>(!!getToken());
  // SSO logins carry no localStorage token (the session lives in an HttpOnly
  // cookie) — probe /auth/me once on mount so a page load after an OIDC
  // redirect (or a refresh mid-session) doesn't bounce back to the login page.
  const [checkingSession, setCheckingSession] = useState(!getToken());
  const [helpRailCollapsed, setHelpRailCollapsed] = useState(
    () => localStorage.getItem(HELP_RAIL_COLLAPSED_KEY) === "1",
  );
  const navigate = useNavigate();
  const location = useLocation();

  useEffect(() => {
    if (getToken()) return;
    api.get("/ai/gateway/auth/me")
      .then(() => setAuthed(true))
      .catch(() => {})
      .finally(() => setCheckingSession(false));
  }, []);

  const toggleLang = () => {
    const next: Lang = lang === "en" ? "zh" : "en";
    setLang(next);
    setLangState(next);
  };

  const toggleHelpRail = () => {
    const next = !helpRailCollapsed;
    setHelpRailCollapsed(next);
    localStorage.setItem(HELP_RAIL_COLLAPSED_KEY, next ? "1" : "0");
  };

  if (checkingSession) {
    return <div className="login-wrap" />;
  }

  if (!authed) {
    return <Login lang={lang} onLogin={() => setAuthed(true)} onToggleLang={toggleLang} />;
  }

  const logout = () => {
    clearToken();
    api.post("/ai/gateway/auth/logout").catch(() => {});
    setAuthed(false);
    navigate("/");
  };

  return (
    <div className="layout">
      <nav className="sidebar" aria-label="Primary">
        <div className="brand">
          <BrandMark size={22} className="brand-mark" />
          <div>
            <div className="brand-name">ai-gateway</div>
            <div className="brand-sub">Console</div>
          </div>
        </div>

        <div className="nav-eyebrow">{t("navOperate", lang)}</div>
        {NAV.slice(0, 3).map((n) => (
          <NavLink key={n.to} to={n.to} end={n.end}>
            <Icon name={n.icon} size={16} /> {t(n.key, lang)}
          </NavLink>
        ))}
        <div className="nav-eyebrow">{t("navManage", lang)}</div>
        {NAV.slice(3, 11).map((n) => (
          <NavLink key={n.to} to={n.to}>
            <Icon name={n.icon} size={16} /> {t(n.key, lang)}
          </NavLink>
        ))}
        <div className="nav-eyebrow">{t("navObserve", lang)}</div>
        {NAV.slice(11).map((n) => (
          <NavLink key={n.to} to={n.to}>
            <Icon name={n.icon} size={16} /> {t(n.key, lang)}
          </NavLink>
        ))}

        <div className="spacer" />
        <div className="foot">
          <button className="iconbtn" onClick={toggleLang} title={lang === "en" ? "中文" : "English"}>
            <Icon name="globe" size={15} />
            <span className="mono" style={{ fontSize: 11 }}>{lang === "en" ? "EN" : "中"}</span>
          </button>
          <button className="iconbtn" onClick={logout} aria-label={t("logout", lang)} title={t("logout", lang)}>
            <Icon name="logout" size={15} />
          </button>
        </div>
      </nav>

      <main className="main">
        <Routes>
          <Route path="/" element={<Dashboard lang={lang} />} />
          <Route path="/keys" element={<Keys lang={lang} />} />
          <Route path="/providers" element={<Providers lang={lang} />} />
          <Route path="/mcp-servers" element={<McpServers lang={lang} />} />
          <Route path="/models-pricing" element={<ModelsPricing lang={lang} />} />
          <Route path="/model-mappings" element={<ModelMappings lang={lang} />} />
          <Route path="/guardrail-policies" element={<GuardrailPolicies lang={lang} />} />
          <Route path="/audit" element={<Audit lang={lang} />} />
          <Route path="/tenants" element={<Tenants lang={lang} />} />
          <Route path="/billing" element={<Billing lang={lang} />} />
          <Route path="/users" element={<Users lang={lang} />} />
          <Route path="/settings" element={<Settings lang={lang} />} />
          <Route path="/usage" element={<Usage lang={lang} />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </main>

      {HELP_CONTENT[location.pathname] && (
        <HelpRail
          icon={HELP_CONTENT[location.pathname].icon}
          title={t("helpRailTitle", lang)}
          tips={HELP_CONTENT[location.pathname].tips.map((tip) => ({
            title: t(tip.titleKey, lang),
            body: t(tip.bodyKey, lang),
          }))}
          collapsed={helpRailCollapsed}
          onToggle={toggleHelpRail}
          collapseLabel={t("helpRailCollapse", lang)}
          expandLabel={t("helpRailExpand", lang)}
        />
      )}
    </div>
  );
}

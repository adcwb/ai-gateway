import { useState } from "react";
import { Navigate, NavLink, Route, Routes, useNavigate } from "react-router-dom";
import { clearToken, getToken } from "./api/client";
import { getLang, setLang, t, type Lang } from "./i18n";
import Login from "./pages/Login";
import Dashboard from "./pages/Dashboard";
import Keys from "./pages/Keys";
import Providers from "./pages/Providers";
import Audit from "./pages/Audit";
import Tenants from "./pages/Tenants";
import Billing from "./pages/Billing";

export default function App() {
  const [lang, setLangState] = useState<Lang>(getLang());
  const [authed, setAuthed] = useState<boolean>(!!getToken());
  const navigate = useNavigate();

  const toggleLang = () => {
    const next: Lang = lang === "en" ? "zh" : "en";
    setLang(next);
    setLangState(next);
  };

  if (!authed) {
    return <Login lang={lang} onLogin={() => setAuthed(true)} onToggleLang={toggleLang} />;
  }

  const logout = () => {
    clearToken();
    setAuthed(false);
    navigate("/");
  };

  return (
    <div className="layout">
      <nav className="sidebar">
        <div className="brand">⛩ ai-gateway</div>
        <NavLink to="/" end>{t("dashboard", lang)}</NavLink>
        <NavLink to="/keys">{t("keys", lang)}</NavLink>
        <NavLink to="/providers">{t("providers", lang)}</NavLink>
        <NavLink to="/audit">{t("audit", lang)}</NavLink>
        <NavLink to="/tenants">{t("tenants", lang)}</NavLink>
        <NavLink to="/billing">{t("billing", lang)}</NavLink>
        <div className="spacer" />
        <div className="foot">
          <button className="ghost" onClick={toggleLang}>{lang === "en" ? "中文" : "EN"}</button>
          <button className="ghost" onClick={logout}>{t("logout", lang)}</button>
        </div>
      </nav>
      <main className="main">
        <Routes>
          <Route path="/" element={<Dashboard lang={lang} />} />
          <Route path="/keys" element={<Keys lang={lang} />} />
          <Route path="/providers" element={<Providers lang={lang} />} />
          <Route path="/audit" element={<Audit lang={lang} />} />
          <Route path="/tenants" element={<Tenants lang={lang} />} />
          <Route path="/billing" element={<Billing lang={lang} />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </main>
    </div>
  );
}

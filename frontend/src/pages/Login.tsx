import { useEffect, useState } from "react";
import { api, setToken, type AuthConfig } from "../api/client";
import { t, type Lang } from "../i18n";
import { Icon, type IconName } from "../components/ui";

interface Props {
  lang: Lang;
  onLogin: () => void;
  onToggleLang: () => void;
}

const FEATURES: { icon: IconName; key: "keys" | "providers" | "billing" | "audit" }[] = [
  { icon: "key", key: "keys" },
  { icon: "providers", key: "providers" },
  { icon: "billing", key: "billing" },
  { icon: "audit", key: "audit" },
];

export default function Login({ lang, onLogin, onToggleLang }: Props) {
  const [token, setTokenInput] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [oidcEnabled, setOidcEnabled] = useState(false);

  useEffect(() => {
    api.get<AuthConfig>("/ai/gateway/auth/config").then((c) => setOidcEnabled(c.oidcEnabled)).catch(() => {});
  }, []);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    setToken(token.trim());
    try {
      // Validate the token against a cheap authenticated endpoint.
      await api.get("/ai/gateway/key/stats");
      onLogin();
    } catch {
      setError(t("unauthorized", lang));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="login-wrap">
      <aside className="login-pane">
        <div className="pane-deco"><Icon name="torii" size={320} /></div>
        <div className="pane-brand">
          <span className="pane-mark"><Icon name="torii" size={26} /></span>
          <div>
            <div className="pane-title">{t("appName", lang)}</div>
            <div className="brand-sub">Operator Console</div>
          </div>
        </div>
        <p className="pane-sub">{t("loginTagline", lang)}</p>
        <div className="pane-feats">
          {FEATURES.map((f) => (
            <div className="feat" key={f.key}>
              <span className="chip"><Icon name={f.icon} size={14} /></span>
              {t(f.key, lang)}
            </div>
          ))}
        </div>
      </aside>

      <main className="login-form">
        <form className="login-box" onSubmit={submit}>
          <h1>{t("login", lang)}</h1>
          <p className="hint">{t("loginHint", lang)}</p>
          <label className="field">
            <div className="field-label">{t("adminToken", lang)}</div>
            <input
              type="password"
              placeholder={t("adminToken", lang)}
              value={token}
              onChange={(e) => setTokenInput(e.target.value)}
              autoFocus
            />
          </label>
          {error && <div className="error-text">{error}</div>}
          <button type="submit" disabled={busy}>
            {busy ? <Icon name="refresh" size={14} className="spin" /> : <Icon name="logout" size={14} />}{" "}
            {t("login", lang)}
          </button>
          {oidcEnabled && (
            <>
              <div className="sub" style={{ textAlign: "center", margin: "4px 0" }}>{t("or", lang)}</div>
              <button type="button" className="ghost" onClick={() => { window.location.href = "/ai/gateway/auth/login"; }}>
                <Icon name="globe" size={14} /> {t("ssoLogin", lang)}
              </button>
            </>
          )}
          <button type="button" className="ghost" onClick={onToggleLang}>
            <Icon name="globe" size={14} /> {lang === "en" ? "中文" : "English"}
          </button>
        </form>
      </main>
    </div>
  );
}

import { useEffect, useState } from "react";
import { api, setToken, type AuthConfig } from "../api/client";
import { t, type Lang } from "../i18n";
import { BrandMark, Button, Field, Icon, Tabs, type IconName } from "../components/ui";

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
  const [method, setMethod] = useState<"token" | "sso">("token");

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
        <div className="pane-deco"><BrandMark size={320} strokeWidth={1.4} /></div>
        <div className="pane-brand">
          <span className="pane-mark"><BrandMark size={26} /></span>
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
        <button type="button" className="login-lang" onClick={onToggleLang}>
          <Icon name="globe" size={13} /> {lang === "en" ? "中文" : "English"}
        </button>
        <div className="login-card">
          <h1>{t("login", lang)}</h1>
          {oidcEnabled && <p className="hint">{t("loginHintMulti", lang)}</p>}

          {oidcEnabled && (
            <Tabs
              items={[
                { key: "token", label: t("adminToken", lang) },
                { key: "sso", label: t("loginMethodSso", lang) },
              ]}
              active={method}
              onChange={(k) => setMethod(k as "token" | "sso")}
            />
          )}

          {(!oidcEnabled || method === "token") && (
            <form onSubmit={submit}>
              <p className="hint mb-16">{t("loginHint", lang)}</p>
              <Field label={t("adminToken", lang)}>
                <input
                  type="password"
                  placeholder={t("adminToken", lang)}
                  value={token}
                  onChange={(e) => setTokenInput(e.target.value)}
                  autoFocus
                />
              </Field>
              {error && <div className="error-text mt-8">{error}</div>}
              <Button type="submit" disabled={busy} style={{ marginTop: 14, width: "100%" }}>
                {busy ? <Icon name="refresh" size={14} className="spin" /> : <Icon name="logout" size={14} />}{" "}
                {t("login", lang)}
              </Button>
            </form>
          )}

          {oidcEnabled && method === "sso" && (
            <div className="login-sso">
              <p className="hint">{t("ssoHint", lang)}</p>
              <Button type="button" style={{ width: "100%" }} onClick={() => { window.location.href = "/ai/gateway/auth/login"; }}>
                <Icon name="globe" size={14} /> {t("ssoLogin", lang)}
              </Button>
            </div>
          )}
        </div>

        <p className="login-trust">{t("loginTrust", lang)}</p>
      </main>
    </div>
  );
}

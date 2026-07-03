import { useState } from "react";
import { api, setToken } from "../api/client";
import { t, type Lang } from "../i18n";

interface Props {
  lang: Lang;
  onLogin: () => void;
  onToggleLang: () => void;
}

export default function Login({ lang, onLogin, onToggleLang }: Props) {
  const [token, setTokenInput] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

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
      <form className="login-box" onSubmit={submit}>
        <h1>{t("appName", lang)}</h1>
        <p className="hint">{t("loginHint", lang)}</p>
        <input
          type="password"
          placeholder={t("adminToken", lang)}
          value={token}
          onChange={(e) => setTokenInput(e.target.value)}
          autoFocus
        />
        {error && <div className="error-text">{error}</div>}
        <button type="submit" disabled={busy}>
          {t("login", lang)}
        </button>
        <button type="button" className="ghost" onClick={onToggleLang}>
          {lang === "en" ? "中文" : "English"}
        </button>
      </form>
    </div>
  );
}

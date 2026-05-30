package server

import (
	"html/template"
	"net/http"
)

var loginTmpl = template.Must(template.New("login").Parse(loginHTML))

func renderLogin(w http.ResponseWriter, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = loginTmpl.Execute(w, map[string]string{"Error": errMsg})
}

const loginHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>sign in &middot; web terminal</title>
<style>
  :root { color-scheme: dark; }
  html,body{height:100%;margin:0}
  body{display:flex;align-items:center;justify-content:center;background:#0b0b0e;
       font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;color:#e6e6e6}
  .card{width:320px;max-width:88vw;background:#15151b;border:1px solid #2a2a35;
        border-radius:12px;padding:26px}
  h1{font-size:17px;font-weight:600;margin:0 0 4px}
  p{font-size:13px;color:#9a9aa6;margin:0 0 18px}
  label{display:block;font-size:12px;color:#9a9aa6;margin:0 0 6px}
  input{width:100%;box-sizing:border-box;background:#0b0b0e;border:1px solid #2a2a35;
        border-radius:8px;color:#e6e6e6;padding:10px 12px;font-size:14px;
        font-family:ui-monospace,Consolas,monospace}
  input:focus{outline:none;border-color:#6b6bff}
  button{width:100%;margin-top:14px;background:#6b6bff;border:0;border-radius:8px;
         color:#fff;padding:11px;font-size:14px;font-weight:600;cursor:pointer}
  button:hover{background:#5a5af0}
  .err{background:#3a1518;border:1px solid #7a2a2f;color:#ffb3b8;font-size:13px;
       border-radius:8px;padding:9px 11px;margin:0 0 14px}
</style>
</head>
<body>
  <form class="card" method="POST" action="/login">
    <h1>web terminal</h1>
    <p>paste your access token to continue</p>
    {{if .Error}}<div class="err">{{.Error}}</div>{{end}}
    <label for="token">access token</label>
    <input id="token" name="token" type="password" autocomplete="off" autofocus placeholder="token">
    <button type="submit">sign in</button>
  </form>
</body>
</html>`

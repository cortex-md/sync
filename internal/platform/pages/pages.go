package pages

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

//go:embed templates/pages.html
var pagesFS embed.FS

//go:embed assets
var assetsFS embed.FS

var pageTemplates = template.Must(template.ParseFS(pagesFS, "templates/pages.html"))

const pageCSP = "default-src 'none'; style-src 'self'; font-src 'self'; img-src 'self'; script-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"

const AppURL template.URL = "cortex://"

var pageStrings = map[string]map[string]string{
	"en": {
		"verify.title":        "Confirm your email",
		"verify.message":      "Confirm that this email address belongs to your Cortex account.",
		"verify.button":       "Confirm email",
		"verify.done.title":   "Email verified",
		"verify.done":         "Thanks — %s is confirmed. You can go back to Cortex.",
		"change.title":        "Confirm your new email",
		"change.message":      "Confirm the new email address of your Cortex account.",
		"change.button":       "Confirm new email",
		"change.done.title":   "Email changed",
		"change.done":         "%s is now the email of your Cortex account. Use it the next time you sign in.",
		"change.taken":        "This email is already in use by another account.",
		"reset.title":         "Choose a new password",
		"reset.for":           "For %s",
		"reset.message":       "Open Cortex on this device to choose a new password. Your other devices will be signed out.",
		"reset.button":        "Open Cortex",
		"reset.token":         "If Cortex doesn't open, paste this reset code in the app",
		"reset.fine":          "Didn't ask for this? Close this page — your password stays the same.",
		"reset.done.title":    "Password changed",
		"reset.done":          "Your password was updated. Other devices were signed out to keep your account safe.",
		"reset.done.fine":     "Wasn't you? Reset your password again right away.",
		"reset.invalid.title": "Password too short",
		"reset.invalid":       "The password must have at least 8 characters.",
		"expired.title":       "This link has expired",
		"expired.resend":      "Links are valid for a short time to keep your account safe. Get a fresh one below.",
		"expired.app":         "Links are valid for a short time to keep your account safe. Request a new one from Settings → Account in Cortex.",
		"expired.email":       "Email",
		"expired.button":      "Send a new link",
		"sent.title":          "Check your inbox",
		"sent.message":        "If an account exists for %s, we sent a new link. It works for 1 hour.",
		"sent.fine":           "Didn't arrive? Check spam, or try again in a minute.",
		"invite.title":        "%s invited you to a vault",
		"invite.members":      "%d members · You'll join as %s",
		"invite.member":       "%d member · You'll join as %s",
		"invite.accept":       "Accept invite",
		"invite.decline":      "Decline",
		"invite.fine":         "Invite expires %s. Don't have Cortex yet? You'll be able to download it after accepting.",
		"invite.declined":     "Invite declined",
		"invite.declined.msg": "You can ask %s for a new invite any time.",
		"invite.unusable":     "This invite can't be used",
		"invite.invalid":      "This invite link is not valid. It may have been cancelled or replaced by a newer invite.",
		"invite.expired":      "This invite has expired. Ask for a new invite.",
		"invite.used":         "This invite was already accepted. Open Cortex to see the vault.",
		"share.unavailable":   "This note is unavailable",
		"share.invalid":       "This note is no longer shared, or the link has expired.",
		"open.cortex":         "Open Cortex",
		"error.title":         "Something went wrong",
		"error.message":       "Try again in a few minutes.",
		"role.owner":          "Owner",
		"role.admin":          "Admin",
		"role.editor":         "Editor",
		"role.viewer":         "Viewer",
		"role.guest":          "Guest",
	},
	"pt-BR": {
		"verify.title":        "Confirme seu e-mail",
		"verify.message":      "Confirme que este endereço de e-mail pertence à sua conta no Cortex.",
		"verify.button":       "Confirmar e-mail",
		"verify.done.title":   "E-mail verificado",
		"verify.done":         "Obrigado — %s está confirmado. Você já pode voltar ao Cortex.",
		"change.title":        "Confirme seu novo e-mail",
		"change.message":      "Confirme o novo endereço de e-mail da sua conta no Cortex.",
		"change.button":       "Confirmar novo e-mail",
		"change.done.title":   "E-mail alterado",
		"change.done":         "%s agora é o e-mail da sua conta no Cortex. Use-o no próximo login.",
		"change.taken":        "Este e-mail já está em uso por outra conta.",
		"reset.title":         "Escolha uma nova senha",
		"reset.for":           "Para %s",
		"reset.message":       "Abra o Cortex neste dispositivo para escolher uma nova senha. Seus outros dispositivos serão desconectados.",
		"reset.button":        "Abrir o Cortex",
		"reset.token":         "Se o Cortex não abrir, cole este código de redefinição no app",
		"reset.fine":          "Não pediu isso? Feche esta página — sua senha continua a mesma.",
		"reset.done.title":    "Senha alterada",
		"reset.done":          "Sua senha foi atualizada. Os outros dispositivos foram desconectados para manter sua conta segura.",
		"reset.done.fine":     "Não foi você? Redefina sua senha novamente agora.",
		"reset.invalid.title": "Senha muito curta",
		"reset.invalid":       "A senha precisa ter pelo menos 8 caracteres.",
		"expired.title":       "Este link expirou",
		"expired.resend":      "Os links valem por pouco tempo para manter sua conta segura. Peça um novo abaixo.",
		"expired.app":         "Os links valem por pouco tempo para manter sua conta segura. Peça um novo em Configurações → Conta no Cortex.",
		"expired.email":       "E-mail",
		"expired.button":      "Enviar novo link",
		"sent.title":          "Confira sua caixa de entrada",
		"sent.message":        "Se existir uma conta para %s, enviamos um novo link. Ele vale por 1 hora.",
		"sent.fine":           "Não chegou? Confira o spam ou tente de novo em um minuto.",
		"invite.title":        "%s convidou você para um vault",
		"invite.members":      "%d membros · Você vai entrar como %s",
		"invite.member":       "%d membro · Você vai entrar como %s",
		"invite.accept":       "Aceitar convite",
		"invite.decline":      "Recusar",
		"invite.fine":         "O convite expira em %s. Ainda não tem o Cortex? Você poderá baixá-lo depois de aceitar.",
		"invite.declined":     "Convite recusado",
		"invite.declined.msg": "Você pode pedir um novo convite para %s quando quiser.",
		"invite.unusable":     "Este convite não pode ser usado",
		"invite.invalid":      "Este link de convite não é válido. Ele pode ter sido cancelado ou substituído por um convite mais novo.",
		"invite.expired":      "Este convite expirou. Peça um novo convite.",
		"invite.used":         "Este convite já foi aceito. Abra o Cortex para ver o vault.",
		"share.unavailable":   "Esta nota não está disponível",
		"share.invalid":       "Esta nota não está mais compartilhada ou o link expirou.",
		"open.cortex":         "Abrir o Cortex",
		"error.title":         "Algo deu errado",
		"error.message":       "Tente novamente em alguns minutos.",
		"role.owner":          "Proprietário",
		"role.admin":          "Admin",
		"role.editor":         "Editor",
		"role.viewer":         "Leitor",
		"role.guest":          "Convidado",
	},
}

type Action struct {
	Label    string
	URL      template.URL
	Post     string
	Style    string
	KeepHash bool
}

type Vault struct {
	Name   string
	Detail string
}

type EmailForm struct {
	Action string
	Label  string
	Button string
}

type Data struct {
	Lang      string
	Icon      string
	Tone      string
	Title     string
	Detail    string
	Message   string
	Fine      string
	Token     string
	Actions   []Action
	Vault     *Vault
	EmailForm *EmailForm
	Code      string
	CodeLabel string
}

func Locale(r *http.Request) string {
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Accept-Language")), "pt") {
		return "pt-BR"
	}
	return "en"
}

func Text(locale, key string) string {
	return pageStrings[locale][key]
}

func Textf(locale, key string, args ...any) string {
	return fmt.Sprintf(pageStrings[locale][key], args...)
}

func Role(locale, role string) string {
	if label := pageStrings[locale]["role."+role]; label != "" {
		return label
	}
	return role
}

func Date(locale string, t time.Time) string {
	if locale == "pt-BR" {
		return t.UTC().Format("02/01/2006")
	}
	return t.UTC().Format("Jan 2, 2006")
}

func OpenCortex(locale string) Action {
	return Action{Label: Text(locale, "open.cortex"), URL: AppURL, Style: "primary"}
}

func Render(w http.ResponseWriter, status int, data Data) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", pageCSP)
	w.WriteHeader(status)
	if err := pageTemplates.ExecuteTemplate(w, "card", data); err != nil {
		log.Error().Err(err).Str("page", data.Title).Msg("failed to render page")
	}
}

func Notice(w http.ResponseWriter, r *http.Request, status int, titleKey, messageKey string) {
	locale := Locale(r)
	Render(w, status, Data{
		Lang:    locale,
		Icon:    "clock",
		Tone:    "warn",
		Title:   Text(locale, titleKey),
		Message: Text(locale, messageKey),
	})
}

func Error(w http.ResponseWriter, r *http.Request) {
	locale := Locale(r)
	Render(w, http.StatusInternalServerError, Data{
		Lang:    locale,
		Icon:    "alert",
		Tone:    "warn",
		Title:   Text(locale, "error.title"),
		Message: Text(locale, "error.message"),
	})
}

func Assets() http.Handler {
	assets, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		panic(err)
	}
	files := http.StripPrefix("/assets/v1/", http.FileServerFS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		files.ServeHTTP(w, r)
	})
}

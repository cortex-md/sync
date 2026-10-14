"use strict"

const CONTENT_CSS = `
html { background: transparent; }
body { margin: 0; background: transparent; color: var(--text-secondary); font: 400 17px/30px var(--font-editor); overflow-wrap: anywhere; -webkit-font-smoothing: antialiased; }
main > :first-child { margin-top: 0 !important; }
h1, h2, h3, h4, h5, h6 { scroll-margin-top: 80px; }
h1 { margin: 40px 0 8px; color: var(--text-primary); font: 700 30px/1.2 var(--font-editor); letter-spacing: -0.02em; }
h2 { margin: 40px 0 8px; color: var(--h2-color); font: 600 22px/1.3 var(--font-editor); }
h3 { margin: 32px 0 6px; color: var(--text-primary); font: 600 19px/1.35 var(--font-editor); }
h4, h5, h6 { margin: 24px 0 6px; color: var(--text-primary); font: 600 17px/1.4 var(--font-editor); }
p { margin: 0 0 16px; }
ul, ol { margin: 0 0 16px; padding-left: 24px; }
li { padding: 3px 0; }
li:has(> input[type="checkbox"]) { list-style: none; margin-left: -24px; }
input[type="checkbox"] { width: 16px; height: 16px; margin: 0 12px 0 0; vertical-align: -2px; accent-color: var(--accent); }
li:has(> input[type="checkbox"]:checked) { color: var(--text-muted); text-decoration: line-through; }
a { color: var(--link); text-decoration: none; border-bottom: 1px solid color-mix(in srgb, var(--link) 35%, transparent); }
strong { color: var(--text-primary); }
blockquote { display: block; margin: 20px 0; padding: 14px 16px; border-radius: 12px; background: var(--callout-note-bg); color: var(--text-secondary); font: 400 15px/24px var(--font-editor); }
blockquote > :last-child { margin-bottom: 0; }
code { padding: 2px 6px; border-radius: 6px; background: color-mix(in srgb, var(--text-primary) 4.5%, transparent); color: var(--text-primary); font: 400 14px "Geist Mono", var(--font-code); }
pre { margin: 20px 0; padding: 16px 18px; border-radius: 14px; background: var(--bg-elevated); box-shadow: 0 0 0 1px color-mix(in srgb, var(--text-primary) 7%, transparent); color: var(--text-primary); font: 400 13.5px/22px "Geist Mono", var(--font-code); overflow-x: auto; }
pre code { padding: 0; background: transparent; font: inherit; }
img { max-width: 100%; height: auto; border-radius: 10px; }
table { display: block; margin: 20px 0; border-collapse: collapse; overflow-x: auto; font-size: 15px; }
th, td { padding: 8px 12px; border: 1px solid color-mix(in srgb, var(--text-primary) 9%, transparent); text-align: left; }
th { color: var(--text-primary); font-weight: 600; }
hr { margin: 32px 0; border: 0; border-top: 1px solid color-mix(in srgb, var(--text-primary) 9%, transparent); }
`

const STRINGS = {
	en: {
		loading: "Decrypting note…",
		unavailable: "This note is unavailable",
		missingKey: "This link is incomplete. Ask for the full link, including the part after #.",
		notFound: "This note is no longer shared, or the link has expired.",
		failed: "This note could not be opened. The link may be wrong or outdated.",
		copy: "Copy link",
		copied: "Copied",
		toDark: "Switch to dark",
		toLight: "Switch to light",
		updated: "Updated",
		minRead: "min read",
		onThisPage: "On this page",
		publishedWith: "Published with",
		content: "Note content",
	},
	"pt-BR": {
		loading: "Descriptografando a nota…",
		unavailable: "Esta nota não está disponível",
		missingKey: "Este link está incompleto. Peça o link inteiro, incluindo a parte depois do #.",
		notFound: "Esta nota não está mais compartilhada ou o link expirou.",
		failed: "Não foi possível abrir esta nota. O link pode estar errado ou desatualizado.",
		copy: "Copiar link",
		copied: "Copiado",
		toDark: "Mudar para o escuro",
		toLight: "Mudar para o claro",
		updated: "Atualizada em",
		minRead: "min de leitura",
		onThisPage: "Nesta página",
		publishedWith: "Publicado com",
		content: "Conteúdo da nota",
	},
}

const SUN_ICON =
	"M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8z"
const MOON_ICON = "M12 3a6 6 0 0 0 9 9 9 9 0 1 1-9-9Z"
const SAFE_IMAGE_TYPE = /^image\/(png|jpe?g|gif|webp|avif|bmp|svg\+xml)$/
const BASE64 = /^[A-Za-z0-9+/]*={0,2}$/
const WORDS_PER_MINUTE = 200

const root = document.getElementById("share")
const text = STRINGS[root.dataset.lang] ?? STRINGS.en
const frame = document.getElementById("share-content")
const themeButton = document.getElementById("share-theme")
let theme = window.matchMedia("(prefers-color-scheme: dark)").matches ? "ink" : "paper"

function decodeBase64Url(value) {
	const base64 = value.replace(/-/g, "+").replace(/_/g, "/")
	const padded = base64.padEnd(Math.ceil(base64.length / 4) * 4, "=")
	return Uint8Array.from(atob(padded), (char) => char.charCodeAt(0))
}

async function decryptBundle(blob, rawKey) {
	const key = await crypto.subtle.importKey("raw", rawKey, "AES-GCM", false, ["decrypt"])
	const compressed = await crypto.subtle.decrypt({ name: "AES-GCM", iv: blob.slice(0, 12) }, key, blob.slice(12))
	const stream = new Blob([compressed]).stream().pipeThrough(new DecompressionStream("gzip"))
	return JSON.parse(await new Response(stream).text())
}

function attachmentSource(bundle, index) {
	const attachment = Array.isArray(bundle.attachments) ? bundle.attachments[Number(index)] : null
	if (!attachment || !SAFE_IMAGE_TYPE.test(attachment.type) || !BASE64.test(attachment.data)) return ""
	return `data:${attachment.type};base64,${attachment.data}`
}

function renderDocument(bundle) {
	const body = String(bundle.html).replace(/cortex-attachment:(\d+)/g, (_, index) => attachmentSource(bundle, index))
	return `<!doctype html><html data-theme="${theme}"><head><meta charset="utf-8"><base target="_blank"><link rel="stylesheet" href="/assets/v1/cortex.css"><style>${CONTENT_CSS}</style></head><body><main>${body}</main></body></html>`
}

function applyTheme() {
	document.documentElement.dataset.theme = theme
	root.dataset.theme = theme
	if (frame.contentDocument?.documentElement) frame.contentDocument.documentElement.dataset.theme = theme
	const ink = theme === "ink"
	document.getElementById("share-theme-icon").setAttribute("d", ink ? SUN_ICON : MOON_ICON)
	themeButton.setAttribute("aria-label", ink ? text.toLight : text.toDark)
}

function showError(message) {
	document.getElementById("share-loading").hidden = true
	document.getElementById("share-error-title").textContent = text.unavailable
	document.getElementById("share-error-message").textContent = message
	document.getElementById("share-error").hidden = false
}

function initials(name) {
	return name
		.split(/\s+/)
		.filter(Boolean)
		.slice(0, 2)
		.map((part) => part[0].toUpperCase())
		.join("")
}

function metaItem(container, value, className) {
	const item = document.createElement("span")
	item.textContent = value
	if (className) item.className = className
	container.append(item)
}

function renderHeader(bundle) {
	const author = typeof bundle.author === "string" ? bundle.author.trim() : ""
	if (author) {
		const avatar = document.getElementById("share-avatar")
		avatar.textContent = initials(author)
		avatar.hidden = false
		document.getElementById("share-brand").hidden = true
		document.getElementById("share-author").textContent = author
	}
	document.title = `${bundle.title} · Cortex`
	document.getElementById("share-title").textContent = bundle.title
	frame.title = text.content

	const meta = document.getElementById("share-meta")
	const updated = new Date(root.dataset.updated)
	if (!Number.isNaN(updated.getTime())) {
		const date = new Intl.DateTimeFormat(root.dataset.lang, { dateStyle: "medium" }).format(updated)
		metaItem(meta, `${text.updated} ${date}`)
	}
	const words = String(bundle.markdown ?? "").split(/\s+/).filter(Boolean).length
	if (meta.childElementCount > 0) metaItem(meta, "·")
	metaItem(meta, `${Math.max(1, Math.round(words / WORDS_PER_MINUTE))} ${text.minRead}`)
	const tags = Array.isArray(bundle.tags) ? bundle.tags.filter((tag) => typeof tag === "string") : []
	for (const tag of tags.slice(0, 8)) metaItem(meta, `#${tag}`, "cx-tag")
}

function fitFrame() {
	const content = frame.contentDocument
	if (!content?.documentElement) return
	frame.style.height = `${content.documentElement.scrollHeight}px`
}

function headingTop(heading) {
	return frame.getBoundingClientRect().top + window.scrollY + heading.getBoundingClientRect().top - 80
}

function renderToc() {
	const content = frame.contentDocument
	const headings = [...content.querySelectorAll("main h2")].filter((heading) => heading.textContent.trim())
	if (headings.length === 0) return
	const list = document.getElementById("share-toc-list")
	const buttons = headings.map((heading) => {
		const button = document.createElement("button")
		button.type = "button"
		button.textContent = heading.textContent.trim()
		button.addEventListener("click", () => window.scrollTo({ top: headingTop(heading), behavior: "smooth" }))
		list.append(button)
		return button
	})
	const markCurrent = () => {
		let current = 0
		headings.forEach((heading, index) => {
			if (headingTop(heading) <= window.scrollY + 1) current = index
		})
		buttons.forEach((button, index) => button.setAttribute("aria-current", String(index === current)))
	}
	document.getElementById("share-toc-title").textContent = text.onThisPage
	document.getElementById("share-toc").setAttribute("aria-label", text.onThisPage)
	document.getElementById("share-toc").hidden = false
	window.addEventListener("scroll", markCurrent, { passive: true })
	markCurrent()
}

function onFrameLoad() {
	applyTheme()
	fitFrame()
	new ResizeObserver(fitFrame).observe(frame.contentDocument.body)
	for (const image of frame.contentDocument.images) image.addEventListener("load", fitFrame)
	renderToc()
}

async function copyLink() {
	const label = document.getElementById("share-copy-label")
	try {
		await navigator.clipboard.writeText(location.href)
		label.textContent = text.copied
		setTimeout(() => {
			label.textContent = text.copy
		}, 2000)
	} catch {}
}

async function main() {
	document.getElementById("share-copy-label").textContent = text.copy
	document.getElementById("share-made").textContent = text.publishedWith
	document.getElementById("share-copy").addEventListener("click", copyLink)
	themeButton.addEventListener("click", () => {
		theme = theme === "ink" ? "paper" : "ink"
		applyTheme()
	})
	applyTheme()

	const key = new URLSearchParams(location.hash.slice(1)).get("k")
	if (!key) {
		showError(text.missingKey)
		return
	}
	document.getElementById("share-loading").textContent = text.loading
	try {
		const response = await fetch(`/share/v1/${encodeURIComponent(root.dataset.shareId)}`, { cache: "no-cache" })
		if (response.status === 404) {
			showError(text.notFound)
			return
		}
		if (!response.ok) throw new Error(`HTTP ${response.status}`)
		const bundle = await decryptBundle(new Uint8Array(await response.arrayBuffer()), decodeBase64Url(key))
		renderHeader(bundle)
		frame.addEventListener("load", onFrameLoad, { once: true })
		frame.srcdoc = renderDocument(bundle)
		document.getElementById("share-state").hidden = true
		document.getElementById("share-layout").hidden = false
	} catch {
		showError(text.failed)
	}
}

main()

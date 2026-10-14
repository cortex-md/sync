"use strict"

for (const link of document.querySelectorAll("a[data-keep-hash]")) {
	link.href = link.getAttribute("href") + window.location.hash
}

for (const input of document.querySelectorAll("input[data-select-on-focus]")) {
	input.addEventListener("focus", () => input.select())
}

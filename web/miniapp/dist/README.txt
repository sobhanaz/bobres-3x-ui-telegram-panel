The Mini App build (npm run build) writes the app to app/ here; the Go
package in web/miniapp embeds this directory into the core binary.
Without a build, core serves a short notice at /app instead.

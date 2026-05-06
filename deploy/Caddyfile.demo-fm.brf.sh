# Caddyfile fragment for demo-fm.brf.sh on the brkip DR-site.
#
# Drop this into /opt/brkip-stack/caddy/Caddyfile.d/demo-fm.brf.sh.caddy
# and reload:
#
#   docker exec brkip-caddy caddy reload --config /etc/caddy/Caddyfile
#
# brkip's Caddy uses an internal CA (tls internal); the public-facing
# DNS A record points at brkip's IP and is NOT proxied (proxied:false).
# WARP / Cloudflare-Tunnel rules fronting brkip are handled by the host
# Cloudflared tunnel — see infrastack docs/all-services.md for the
# ingress map.

demo-fm.brf.sh {
	tls internal

	# Long-lived uploads — multipart presigned PUT goes browser →
	# S3 directly so only init/finalize JSON is proxied, but be
	# generous so a slow 5 GB direct upload still finalizes through
	# the request.
	request_body {
		max_size 5GB
	}

	# Filex needs the real client IP for audit + rate-limiting. Caddy
	# sets X-Forwarded-For + X-Forwarded-Proto + Host automatically
	# under reverse_proxy, but bump trusted_proxies for clarity.
	reverse_proxy 127.0.0.1:5212 {
		header_up X-Real-IP {remote_host}
		transport http {
			read_timeout 600s
			write_timeout 600s
			# WebSocket / SSE upgrade is automatic in caddy v2 reverse_proxy.
		}
	}

	encode zstd gzip

	log {
		output file /var/log/caddy/demo-fm.brf.sh.log {
			roll_size 50MB
			roll_keep 5
		}
		format json
	}
}

# RentLoop — Production Deployment Guide

This guide takes you from zero to a live production server on DigitalOcean.

---

## Prerequisites

- A DigitalOcean account
- A domain name pointed at DigitalOcean DNS (e.g. `rentloop.co.ke`)
- A Twilio account with a WhatsApp-enabled number
- A Safaricom Daraja account with C2B confirmed
- A GitHub repository with Actions enabled

---

## 1. Provision the Droplet

Create a **$12/month Basic Droplet**:

| Setting | Value |
|---|---|
| Image | Ubuntu 24.04 LTS |
| Size | 2 vCPU / 2 GB RAM |
| Region | Frankfurt (fra1) or closest to Kenya |
| Authentication | SSH Key |

```bash
# Add your SSH key during creation, then connect:
ssh root@YOUR_DROPLET_IP
```

---

## 2. Server Hardening

```bash
# Create deploy user
useradd -m -s /bin/bash rentloop
usermod -aG sudo rentloop

# Copy your SSH key to the new user
mkdir -p /home/rentloop/.ssh
cp ~/.ssh/authorized_keys /home/rentloop/.ssh/
chown -R rentloop:rentloop /home/rentloop/.ssh
chmod 700 /home/rentloop/.ssh
chmod 600 /home/rentloop/.ssh/authorized_keys

# Disable root SSH login
sed -i 's/PermitRootLogin yes/PermitRootLogin no/' /etc/ssh/sshd_config
systemctl restart sshd

# Basic firewall
ufw allow OpenSSH
ufw allow 80
ufw allow 443
ufw enable
```

---

## 3. Install PostgreSQL

```bash
apt update && apt install -y postgresql postgresql-contrib

# Create database and user
sudo -u postgres psql <<EOF
CREATE USER rentloop WITH PASSWORD 'STRONG_PASSWORD_HERE';
CREATE DATABASE rentloop OWNER rentloop;
GRANT ALL PRIVILEGES ON DATABASE rentloop TO rentloop;
EOF
```

---

## 4. Install Caddy

```bash
apt install -y debian-keyring debian-archive-keyring apt-transport-https
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' \
  | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
  | tee /etc/apt/sources.list.d/caddy-stable.list
apt update && apt install caddy
```

Create `/etc/caddy/Caddyfile`:

```caddy
api.rentloop.co.ke {
    reverse_proxy 127.0.0.1:8080 {
        header_up X-Real-IP {remote_host}
        header_up X-Forwarded-For {remote_host}
    }
    header {
        X-Frame-Options DENY
        X-Content-Type-Options nosniff
        X-XSS-Protection "1; mode=block"
        Strict-Transport-Security "max-age=31536000; includeSubDomains"
    }
}
```

```bash
systemctl enable caddy
systemctl start caddy
```

Caddy automatically provisions a Let's Encrypt TLS certificate.

---

## 5. Create the Application Directory

```bash
mkdir -p /opt/rentloop/internal/db/migrations
chown -R rentloop:rentloop /opt/rentloop
```

---

## 6. Configure Environment

```bash
# Create environment file (not in git — managed on server only)
cat > /etc/rentloop.env <<'EOF'
PORT=8080
APP_ENV=production
DATABASE_URL=postgres://rentloop:STRONG_PASSWORD_HERE@localhost:5432/rentloop?sslmode=disable
ADMIN_SETUP_SECRET=your-one-time-setup-secret
JWT_SECRET=your-64-char-hex-secret
ACTIVATION_SECRET=your-other-64-char-hex-secret
MPESA_ENV=production
MPESA_CONSUMER_KEY=your_production_consumer_key
MPESA_CONSUMER_SECRET=your_production_consumer_secret
MPESA_PAYBILL=your_paybill_shortcode
MPESA_SHORTCODE=your_paybill_shortcode
MPESA_PASSKEY=your_production_passkey
MPESA_CALLBACK_URL=https://api.rentloop.co.ke/mpesa/stk/callback
TWILIO_SID=ACxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
TWILIO_TOKEN=your_twilio_auth_token
TWILIO_WHATSAPP_FROM=whatsapp:+YOUR_APPROVED_NUMBER
TWILIO_SMS_FROM=+YOUR_SMS_NUMBER
WHATSAPP_WEBHOOK_URL=https://api.rentloop.co.ke/bot/whatsapp
SMS_WEBHOOK_URL=https://api.rentloop.co.ke/bot/sms
DO_SPACES_KEY=your_spaces_key
DO_SPACES_SECRET=your_spaces_secret
DO_SPACES_BUCKET=rentloop-receipts
DO_SPACES_REGION=fra1
DO_SPACES_ENDPOINT=https://fra1.digitaloceanspaces.com
BILLING_GRACE_DAYS=5
SUBSCRIPTION_PRICE_PER_UNIT=50
FREE_TIER_UNIT_LIMIT=3
OWNER_PHONE=+254700000000
EOF

chmod 600 /etc/rentloop.env
chown rentloop:rentloop /etc/rentloop.env
```

---

## 7. Install systemd Service

```bash
cat > /etc/systemd/system/rentloop.service <<'EOF'
[Unit]
Description=RentLoop API Server
After=network.target postgresql.service
Wants=postgresql.service

[Service]
Type=simple
User=rentloop
Group=rentloop
ExecStart=/opt/rentloop/rentloop-server
ExecReload=/bin/kill -HUP $MAINPID
Restart=on-failure
RestartSec=5s
EnvironmentFile=/etc/rentloop.env
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ReadWritePaths=/opt/rentloop
StandardOutput=journal
StandardError=journal
SyslogIdentifier=rentloop

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable rentloop
```

---

## 8. DigitalOcean Spaces Bucket

1. Go to **cloud.digitalocean.com → Spaces Object Storage → Create Space**
2. Name: `rentloop-receipts`
3. Region: `fra1`
4. Access: **Restricted** (objects are uploaded with `public-read` ACL individually)
5. Go to **API → Spaces Access Keys** → Generate new key
6. Add `DO_SPACES_KEY` and `DO_SPACES_SECRET` to `/etc/rentloop.env`

---

## 9. Configure GitHub Actions Secrets

In your GitHub repo → **Settings → Secrets and variables → Actions**, add:

| Secret | Value |
|---|---|
| `DO_HOST` | Your droplet IP |
| `DO_USER` | `rentloop` |
| `DO_SSH_KEY` | Contents of your SSH private key |

---

## 10. First Deploy

```bash
# On your local machine — push to main to trigger CI/CD
git push origin main
```

GitHub Actions will:
1. Run all tests with race detector
2. Build Linux AMD64 binary
3. SCP binary + migrations to droplet
4. Run `rentloop-migrate` on the server
5. `systemctl restart rentloop`
6. Health check with 10 retries (20s window)
7. Auto-rollback if health check fails

---

## 11. Register First Admin

```bash
# On your local machine or browser — one-time only
curl "https://api.rentloop.co.ke/admin/setup?secret=YOUR_ADMIN_SETUP_SECRET"
# Fill in the form to create your admin account
# Then clear ADMIN_SETUP_SECRET from /etc/rentloop.env
```

---

## 12. Register Daraja C2B Callback

In Safaricom Daraja portal → **C2B → Register URL**:

| Field | Value |
|---|---|
| Validation URL | `https://api.rentloop.co.ke/mpesa/c2b/callback` |
| Confirmation URL | `https://api.rentloop.co.ke/mpesa/c2b/callback` |
| Response type | `Completed` |

---

## 13. Configure Twilio Webhooks

In Twilio Console → **Messaging → Senders → WhatsApp sandbox**:

| Field | Value |
|---|---|
| When a message comes in | `https://api.rentloop.co.ke/bot/whatsapp` |
| Method | `POST` |

For SMS → **Phone Numbers → your number → Messaging**:

| Field | Value |
|---|---|
| A message comes in | `https://api.rentloop.co.ke/bot/sms` |

---

## Monitoring

```bash
# Live logs
journalctl -u rentloop -f

# Last 100 lines
journalctl -u rentloop -n 100

# Health check
curl https://api.rentloop.co.ke/health

# Service status
systemctl status rentloop
```

---

## Rollback

If a deployment breaks the service, the CI/CD pipeline auto-rolls back.
For manual rollback:

```bash
ssh rentloop@YOUR_DROPLET_IP
cd /opt/rentloop
cp rentloop-server.prev rentloop-server
sudo systemctl restart rentloop
```

---

## Backup

```bash
# Add to crontab (runs daily at 2am EAT)
0 23 * * * pg_dump -U rentloop rentloop | gzip > /opt/rentloop/backups/rentloop-$(date +\%Y\%m\%d).sql.gz

# Keep 30 days
find /opt/rentloop/backups -name "*.sql.gz" -mtime +30 -delete
```

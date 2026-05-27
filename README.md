# ImmoBot

Automatisierter ImmobilienScout24-Wohnungsbot in Go. Pollt IS24-Suchen, filtert, meldet neue
Inserate per **Telegram** und/oder **WhatsApp** und kann Vermieter optional automatisch über das
IS24-Kontaktformular anschreiben — gesteuert per Chat-Befehl.

> ⚠️ **Disclaimer.** Der Bot umgeht IS24-Bot-Schutz und sendet automatisiert Kontaktanfragen. Das
> verstößt vermutlich gegen die IS24-Nutzungsbedingungen, die WhatsApp-Kopplung kann zur Sperrung
> des WhatsApp-Kontos führen, und das Versenden personenbezogener Daten unterliegt der DSGVO.
> Nutzung auf eigenes Risiko. Starte im Test-Modus, bevor du live gehst.

## Features

- Mehrere Suchprofile parallel (z.B. *Einzelwohnung* + *WG*)
- **Kampagnen**: pro Profil eigenes Nachrichten-Template, KI-Prompt und Bewerberprofil
- Filter: Preis, Zimmer, Fläche, Ort/PLZ, Ausstattung, Baujahr, Ausschluss-Keywords
- Benachrichtigung + Steuerung über Telegram und WhatsApp gleichzeitig
- Optionale KI-Personalisierung der Nachricht (OpenAI)
- Auto-Kontakt via Headless-Chrome (chromedp), mit Anti-Detection (Delays, UA-Rotation)
- Ruhezeiten (nachts kein Versand, aber weiter scrapen)
- Cookie-Ablauf-Warnung + Health-Heartbeat

## Architektur

```
poll (alle 5m) → IS24 scrapen (chromedp) → filtern → SQLite (dedupe)
   → Notify (Telegram + WhatsApp) → [Auto-Kontakt: Formular ausfüllen+absenden]
```

| Paket | Aufgabe |
|-------|---------|
| `scraper/is24` | Headless-Chrome-Scraper (umgeht WAF, Cookie-Auth) |
| `filter` | Suchprofil-Filter |
| `messenger` | Template + OpenAI-Personalisierung |
| `contact` | IS24-Kontaktformular per Browser-Automation |
| `notifier/telegram`, `notifier/whatsapp` | Kanäle (Notify + Befehle) |
| `control` | Transport-neutraler Steuer-State + Befehle |
| `scheduler` | Orchestriert Poll-Loop |
| `repository/sqlite` | Persistenz (pure-Go, kein cgo) |

## Voraussetzungen

- Go **1.25+** (nur für lokalen Build) — oder Docker
- Chrome/Chromium (für `chromedp`; im Docker-Image enthalten)
- Telegram-Bot-Token (via [@BotFather](https://t.me/botfather)) und/oder WhatsApp-Konto
- Gültiger **IS24-Cookie** (eingeloggt)
- Optional: OpenAI-API-Key

## Schnellstart (lokal)

```bash
git clone https://github.com/julianbeese/immo_bot.git
cd immo_bot
cp deployments/.env.example .env      # ausfüllen (siehe unten)
make run                              # baut + startet
make run-once                         # ein einzelner Poll-Zyklus
```

## Konfiguration

Zwei Quellen: `configs/config.yaml` (Verhalten) und **Umgebungsvariablen** (Secrets + persönliche
Daten — bevorzugt, damit nichts Persönliches im Repo landet). Env überschreibt YAML.

### Wichtige Env-Variablen (`.env`)

| Variable | Zweck |
|----------|-------|
| `IS24_COOKIE` | Cookie der eingeloggten IS24-Session (Pflicht fürs Scrapen) |
| `TELEGRAM_ENABLED`, `TELEGRAM_BOT_TOKEN`, `TELEGRAM_CHAT_ID` | Telegram-Kanal |
| `WHATSAPP_ENABLED`, `WHATSAPP_TARGET_PHONE` | WhatsApp-Kanal (Nummer nur Ziffern, z.B. `4915112345678`) |
| `OPENAI_ENABLED`, `OPENAI_API_KEY` | KI-Personalisierung (optional) |
| `CONTACT_ENABLED`, `CONTACT_FIRST_NAME`, `CONTACT_LAST_NAME`, `CONTACT_EMAIL`, `CONTACT_PHONE`, `CONTACT_ADULTS` | Bewerberprofil fürs Kontaktformular |
| `LOG_LEVEL` | `info` oder `debug` |

### IS24-Cookie holen

1. Im Browser bei immobilienscout24.de **einloggen**.
2. DevTools → **Application → Cookies → www.immobilienscout24.de**.
3. Alle Cookies als einen String kopieren: `name1=value1; name2=value2; ...`
4. In `.env` als `IS24_COOKIE=...` setzen.

Cookies laufen ab → bei wiederholt leeren Suchen warnt der Bot („Cookie evtl. abgelaufen"). Dann
neu setzen (siehe `scripts/update_cookie.sh`) und neu starten.

### Kampagnen (`configs/config.yaml`)

Pro Suchstrategie eine Kampagne — eigenes Template, KI-Prompt, optional eigenes Bewerberprofil:

```yaml
default_campaign: "single"
campaigns:
  single:
    message_template_path: "configs/message_single.txt"
    ai_prompt: "Du schreibst für ein berufstätiges Paar ..."
  wg:
    message_template_path: "configs/message_wg.txt"
    ai_prompt: "Du schreibst für eine WG-Gründung ..."
    # contact_profile: { ... }   # KOMPLETT ausfüllen, sonst greift das globale
```

## Telegram einrichten

1. Bot bei [@BotFather](https://t.me/botfather) anlegen → Token.
2. Eigene Chat-ID ermitteln (z.B. via [@userinfobot](https://t.me/userinfobot)).
3. `TELEGRAM_ENABLED=true`, Token + Chat-ID in `.env`.

## WhatsApp verbinden

WhatsApp läuft über **whatsmeow** — der Bot koppelt sich als **verknüpftes Gerät** an dein
WhatsApp-Konto (kein Business-Account nötig). Die Kopplung erfolgt einmalig per QR-Code.

1. In `.env` setzen:
   ```
   WHATSAPP_ENABLED=true
   WHATSAPP_TARGET_PHONE=4915112345678   # deine Nummer, nur Ziffern, ohne + / Leerzeichen
   ```
   `WHATSAPP_TARGET_PHONE` ist gleichzeitig **Empfänger** der Meldungen und die **einzige Nummer,
   die Befehle senden darf** (ein verknüpftes Gerät empfängt alle Chats — das ist die Sicherheitsgrenze).

2. Bot **interaktiv** starten (Terminal nötig für den QR-Code):
   ```bash
   make run
   # oder im Container (siehe Hosting):  docker compose run --rm immobot
   ```

3. Im Terminal erscheint ein QR-Code. In WhatsApp: **Einstellungen → Verknüpfte Geräte → Gerät
   verknüpfen** → QR scannen.

4. Die Session wird in `data/whatsapp.db` gespeichert (persistent). Danach verbindet sich der Bot
   bei Neustarts automatisch — **der QR-Schritt fällt weg**.

> Wichtig: `WHATSAPP_TARGET_PHONE` muss eine **andere** Nummer sein als das gekoppelte Bot-Konto,
> wenn du Befehle senden willst (eigene gesendete Nachrichten erkennt WhatsApp als „von mir" und
> ignoriert sie). Praktisch: koppele ein separates Konto, steuere von deinem Hauptkonto.

## Bot-Befehle (Chat)

Funktionieren in Telegram und WhatsApp, mit oder ohne Slash (`/status` oder `status`):

| Befehl | Wirkung |
|--------|---------|
| `/contact_on` | Auto-Kontakt **live** (sendet echte Anfragen) |
| `/contact_test` | Test-Modus: zeigt Nachricht-Vorschau, sendet nicht (**Standard**) |
| `/contact_off` | Nur beobachten |
| `/quiet_on` / `/quiet_off` | Ruhezeiten an (22–07) / 24-7 |
| `/addprofil [kampagne] <URL> [Name]` | Suchprofil aus IS24-Such-URL anlegen |
| `/listprofile` | Aktive Profile anzeigen |
| `/delprofil <id>` | Profil deaktivieren |
| `/status`, `/stats`, `/help` | Status / Statistik / Hilfe |

### Suchprofil anlegen

Auf immobilienscout24.de die Suche bauen (Stadt, Umkreis, Preis …), URL kopieren:

```
/addprofil single https://www.immobilienscout24.de/Suche/de/berlin/berlin/wohnung-mieten?price=-1500
/addprofil wg     https://www.immobilienscout24.de/Suche/...
```

Mehrere aktive Profile = parallele Suchen, je nach Kampagne unterschiedlich angeschrieben.

## Hosting

Empfehlung: **kleiner VPS + Docker Compose**. Chrome braucht Speicher — plane **≥ 1 GB RAM**
(das Compose-Limit steht auf 512 MB; bei OOM hochsetzen). Ein Hetzner CX22 o.ä. reicht.

### Docker Compose (empfohlen)

```bash
# auf dem Server
git clone https://github.com/julianbeese/immo_bot.git && cd immo_bot
cp deployments/.env.example .env   # ausfüllen

# WhatsApp aktiviert? Einmalig interaktiv koppeln (QR scannen):
docker compose -f deployments/docker-compose.yml run --rm immobot

# danach normal im Hintergrund laufen lassen:
docker compose -f deployments/docker-compose.yml up -d --build
docker compose -f deployments/docker-compose.yml logs -f
```

- Daten (SQLite + WhatsApp-Session) liegen im Volume `immobot_data` → bleiben über Neustarts/
  Rebuilds erhalten.
- `restart: unless-stopped` startet den Bot nach Crash/Reboot neu.
- Healthcheck prüft den letzten erfolgreichen Poll (`immobot -healthcheck`) → `docker ps` zeigt
  `healthy`/`unhealthy`.
- **WhatsApp-Hinweis:** Der QR-Schritt braucht ein TTY. Deshalb **einmal** mit `run --rm` (oben)
  koppeln; der detachte `up -d`-Modus kann keinen QR anzeigen. Nach der Kopplung läuft `up -d`
  ohne Interaktion.

> Falls du nur Env-Variablen für WhatsApp ergänzt: `WHATSAPP_ENABLED` und `WHATSAPP_TARGET_PHONE`
> noch in den `environment:`-Block von `deployments/docker-compose.yml` aufnehmen (oder per
> `config.yaml` setzen).

### Alternative: systemd (nativer Build)

Für Betrieb ohne Docker:

```bash
sudo bash scripts/setup-vps.sh        # installiert Go + Chrome + User (Ubuntu 24.04)
# Projekt nach /opt/immobot kopieren, Binary bauen, .env anlegen
sudo cp deployments/immobot.service /etc/systemd/system/
sudo systemctl enable --now immobot
journalctl -u immobot -f
```

WhatsApp-Erstkopplung hier einmalig im Vordergrund starten (QR scannen), dann den Service aktivieren.

## Betrieb

- **Erst `/contact_test`**, Nachrichten prüfen, dann `/contact_on`. Default ist Test-Modus.
- Logs beobachten; bei „Cookie evtl. abgelaufen"-Warnung Cookie erneuern.
- Statistik per `/stats`.

## Entwicklung

```bash
make build     # binary bauen
make test      # go test ./...
make fmt       # go fmt
make lint      # golangci-lint
```

Pure-Go-SQLite (`modernc.org/sqlite`), Build ist `CGO_ENABLED=0` → statisch, portabel.

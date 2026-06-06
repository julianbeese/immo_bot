"use client"

import * as React from "react"
import { useTheme } from "next-themes"
import { toast, Toaster } from "sonner"
import {
  Moon,
  Sun,
  RefreshCw,
  Plus,
  Trash2,
  ExternalLink,
  ShieldAlert,
  Play,
  Pause,
  Home,
  CheckCircle2,
  CheckCheck,
  Lock,
  BellRing,
  Eye,
  EyeOff,
  Settings,
  Search,
  Building,
  Wand2,
  Save,
  RotateCcw,
  Sparkles,
  FileText,
  LayoutDashboard,
  Mail,
  ListChecks,
  Check,
  X,
  Cookie as CookieIcon
} from "lucide-react"

type View = "overview" | "settings" | "profiles" | "templates" | "listings" | "queue" | "inbox"

const VIEWS: { key: View; label: string; icon: React.ComponentType<{ className?: string }> }[] = [
  { key: "overview",  label: "Übersicht",         icon: LayoutDashboard },
  { key: "settings",  label: "Einstellungen",     icon: Settings },
  { key: "profiles",  label: "Suchprofile",       icon: Building },
  { key: "templates", label: "Nachrichten",       icon: Wand2 },
  { key: "listings",  label: "Wohnungen",         icon: Home },
  { key: "queue",     label: "Queue",             icon: ListChecks },
  { key: "inbox",     label: "Posteingang",       icon: Mail },
]

const STATUS_TONE = {
  active: "border border-border bg-foreground text-background dark:bg-foreground dark:text-background",
  medium: "border border-border bg-muted text-foreground",
  quiet: "border border-border bg-muted/40 text-muted-foreground",
  subtle: "border border-border bg-muted/20 text-muted-foreground",
}

const errorMessage = (error: unknown, fallback: string) =>
  error instanceof Error ? error.message : fallback

import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Badge } from "@/components/ui/badge"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"

// Helper to escape HTML if needed, though React handles escaping automatically
const esc = (s?: string) => s || ""

interface Stats {
  total: number
  notified: number
  contacted: number
}

interface TimingRanges {
  poll_interval_seconds: { min: number; max: number }
  contact_type_delay_ms: { min: number; max: number }
  contact_action_delay_ms: { min: number; max: number }
}

interface Overview {
  contact_mode: "off" | "test" | "approve" | "on"
  contact_label: string
  quiet_hours: boolean
  quiet_hours_start: string
  quiet_hours_end: string
  last_poll: string
  default_campaign: string
  campaigns: string[]
  stats: Stats
  poll_interval_seconds: number
  contact_type_delay_ms: number
  contact_action_delay_ms: number
  timing_ranges: TimingRanges
  exclude_furnished: boolean
}

interface CookieInfo {
  present: boolean
  length: number
  source: "env" | "meta" | "none"
}

interface EmailConfig {
  enabled: boolean
  imap_host: string
  username: string
  password_set: boolean
  password_source: "meta" | "env" | "none"
  mailbox: string
  lookback_hours: number
  senders: string
  meta_override: boolean
  restart_required: boolean
}

// Empty buffer used until the API responds. Keeps form inputs controlled.
const EMPTY_EMAIL_DRAFT = {
  imap_host: "",
  username: "",
  password: "",
  mailbox: "INBOX",
  lookback_hours: 72,
  senders: "",
}

interface SearchProfile {
  id: number
  name: string
  category: string
  search_url?: string
  city?: string
  active: boolean
}

interface CampaignCfg {
  name: string
  ai_prompt: string
  ai_prompt_overridden: boolean
  template: string
  template_overridden: boolean
}

interface InboxMessage {
  id: number
  from: string
  subject: string
  snippet: string
  is24_id?: string
  listing_id?: number
  is_landlord_reply: boolean
  summary?: string
  notified: boolean
  received_at: string
  created_at: string
}

interface InboxScanResult {
  inspected: number            // raw IMAP candidates above the watermark
  fetched: number              // candidates that passed the sender filter
  already_known: number
  classified: number
  landlord_replies: number
  notified: number
  senders: string[] | null
  errors: string[] | null
  duration_ms: number
}

// formatScanDuration renders a Go-side duration as a human-readable label.
// Sub-second scans are common (no new mails → ~150–300ms) and "0.0 s" hides
// the fact that anything happened at all, so we show ms below 1s.
function formatScanDuration(ms: number): string {
  if (ms < 1000) return `${Math.max(1, Math.round(ms))} ms`
  return `${(ms / 1000).toFixed(1)} s`
}

interface Listing {
  id: number
  is24_id?: string
  title: string
  url: string
  address?: string
  city?: string
  district?: string
  postal_code?: string
  price?: number
  price_per_sqm?: number
  rooms?: number
  area?: number
  has_balcony?: boolean
  has_ebk?: boolean
  has_elevator?: boolean
  exclusive_expose?: boolean
  build_year?: number
  available_from?: string
  description?: string
  landlord_name?: string
  contact_person?: string
  search_profile_name?: string
  campaign?: string
  search_profile_id?: number
  notified: boolean
  contacted: boolean
  skipped: boolean
  rejected: boolean
  created_at: string
}

interface SentMessage {
  id: number
  listing_id: number
  is24_id: string
  message: string
  status: string // pending | sent | failed | preview | pending_approval | rejected
  error_msg?: string
  sent_at: string
  created_at: string
}

interface QueuePending {
  sent_message_id: number
  message: string
  created_at: string
  listing: Listing
}

interface QueueData {
  pending: QueuePending | null
  next: Listing[]
}

const EMPTY_OVERVIEW: Overview = {
  contact_mode: "off",
  contact_label: "aus",
  quiet_hours: false,
  quiet_hours_start: "22:00",
  quiet_hours_end: "07:00",
  last_poll: "",
  default_campaign: "",
  campaigns: [],
  stats: { total: 0, notified: 0, contacted: 0 },
  poll_interval_seconds: 1800,
  contact_type_delay_ms: 50,
  contact_action_delay_ms: 1000,
  timing_ranges: {
    poll_interval_seconds: { min: 60, max: 1800 },
    contact_type_delay_ms: { min: 10, max: 500 },
    contact_action_delay_ms: { min: 100, max: 10000 },
  },
  exclude_furnished: true,
}

// DetailRow renders a label + value pair inside the listing detail drawer.
// Label is muted small-caps; value falls back to "–" when empty.
function DetailRow({ label, value }: { label: string; value?: string | number | null }) {
  const display = value === undefined || value === null || value === "" ? "–" : String(value)
  return (
    <div className="flex justify-between gap-2 text-xs">
      <span className="text-muted-foreground">{label}</span>
      <span className="font-semibold text-right">{display}</span>
    </div>
  )
}

// MessageStatusBadge maps a sent_messages.status value to a human label + tone.
// Unknown statuses fall back to the raw string so we never silently hide data.
function MessageStatusBadge({ status }: { status: string }) {
  const map: Record<string, { label: string; tone: string }> = {
    pending:           { label: "in Vorbereitung",  tone: STATUS_TONE.medium },
    pending_approval:  { label: "wartet auf Approval", tone: STATUS_TONE.medium },
    sent:              { label: "✅ gesendet",     tone: STATUS_TONE.active },
    failed:            { label: "❌ Fehler",       tone: STATUS_TONE.quiet },
    preview:           { label: "🧪 Vorschau",    tone: STATUS_TONE.subtle },
    rejected:          { label: "❌ verworfen",   tone: STATUS_TONE.quiet },
  }
  const meta = map[status] ?? { label: status, tone: STATUS_TONE.subtle }
  return (
    <Badge variant="outline" className={`h-5 text-[10px] font-bold px-2 rounded-full ${meta.tone}`}>
      {meta.label}
    </Badge>
  )
}

function sectionSubtitle(view: View, o: Overview): string {
  switch (view) {
    case "overview":  return `${o.stats.total} Wohnungen gefunden, ${o.stats.notified} benachrichtigt, ${o.stats.contacted} kontaktiert.`
    case "settings":  return "Kontakt-Modus, Ruhezeiten und IS24-Cookie."
    case "profiles":  return "IS24-Suchen, die der Bot zyklisch abfragt."
    case "templates": return "AI-Prompt und Nachrichten-Template pro Kampagne."
    case "listings":  return "Gefundene Wohnungen aller Profile."
    case "queue":     return "Approval-Queue: aktueller Vorschlag in Telegram und die nächsten Anwärter."
    case "inbox":     return "IS24-E-Mails: erkannte Anbieter-Antworten außerhalb des Chats."
  }
}

// TimingSlider — controlled range input with live label. onPreview fires while
// dragging (UI only), onCommit fires once on release/blur (POSTs the value).
function TimingSlider({
  label,
  hint,
  value,
  min,
  max,
  step,
  format,
  onPreview,
  onCommit,
}: {
  label: string
  hint: string
  value: number
  min: number
  max: number
  step: number
  format: (v: number) => string
  onPreview: (v: number) => void
  onCommit: (v: number) => void
}) {
  return (
    <div className="space-y-1">
      <div className="flex items-baseline justify-between">
        <label className="text-xs font-semibold">{label}</label>
        <span className="font-mono text-xs tabular-nums text-foreground">{format(value)}</span>
      </div>
      <input
        type="range"
        min={min}
        max={max}
        step={step}
        value={value}
        onChange={(e) => onPreview(Number(e.target.value))}
        onMouseUp={(e) => onCommit(Number((e.target as HTMLInputElement).value))}
        onTouchEnd={(e) => onCommit(Number((e.target as HTMLInputElement).value))}
        onKeyUp={(e) => onCommit(Number((e.target as HTMLInputElement).value))}
        className="w-full h-2 cursor-pointer appearance-none rounded-full bg-muted accent-foreground"
      />
      <p className="text-[11px] text-muted-foreground">{hint}</p>
    </div>
  )
}

export default function DashboardPage() {
  const { resolvedTheme, setTheme } = useTheme()
  
  // Dashboard state
  const [overview, setOverview] = React.useState<Overview | null>(null)
  const [profiles, setProfiles] = React.useState<SearchProfile[]>([])
  const [listings, setListings] = React.useState<Listing[]>([])
  const [inbox, setInbox] = React.useState<InboxMessage[]>([])
  // Posteingang: toggle hides system / info mails, only landlord replies remain.
  // Default false so the user can see why a mail was classified as system at all.
  const [inboxLandlordOnly, setInboxLandlordOnly] = React.useState(false)
  const [inboxScanning, setInboxScanning] = React.useState(false)
  const [inboxScanResult, setInboxScanResult] = React.useState<InboxScanResult | null>(null)
  const [inboxScanError, setInboxScanError] = React.useState<string | null>(null)
  const [queue, setQueue] = React.useState<QueueData>({ pending: null, next: [] })
  const [queueBusy, setQueueBusy] = React.useState<"approve" | "reject" | null>(null)
  const [campaigns, setCampaigns] = React.useState<CampaignCfg[]>([])
  // Editable buffers keyed by campaign name; populated from /api/campaigns.
  const [drafts, setDrafts] = React.useState<Record<string, { ai_prompt: string; template: string }>>({})
  const [savingCampaign, setSavingCampaign] = React.useState<string | null>(null)

  // Cookie status (presence only — the cookie itself is never returned by the API).
  const [cookieInfo, setCookieInfo] = React.useState<CookieInfo | null>(null)
  const [cookieDraft, setCookieDraft] = React.useState("")
  const [savingCookie, setSavingCookie] = React.useState(false)

  // Email config: server state vs draft edits. The IMAP password lives only in
  // EMAIL_PASSWORD env — the API reports password_set so the UI can flag a
  // missing env, but there's no input for it.
  const [emailInfo, setEmailInfo] = React.useState<EmailConfig | null>(null)
  const [emailDraft, setEmailDraft] = React.useState(EMPTY_EMAIL_DRAFT)
  const [emailTouched, setEmailTouched] = React.useState(false)
  const [savingEmail, setSavingEmail] = React.useState(false)

  // Active section selected via the sidebar.
  const [view, setView] = React.useState<View>("overview")
  
  // UI states
  const [loading, setLoading] = React.useState(true)
  const [refreshing, setRefreshing] = React.useState(false)
  const [error, setError] = React.useState<string | null>(null)
  const [searchQuery, setSearchQuery] = React.useState("")
  // Listing filter by search profile id; "all" = no filter.
  const [profileFilter, setProfileFilter] = React.useState<string>("all")
  
  // Listing detail drawer — selected row + lazily-loaded message history.
  const [selectedListing, setSelectedListing] = React.useState<Listing | null>(null)
  const [listingMessages, setListingMessages] = React.useState<SentMessage[]>([])
  const [loadingMessages, setLoadingMessages] = React.useState(false)

  // Right-click context menu for listing rows. `x`/`y` are clientX/clientY
  // captured at the contextmenu event so the menu pops up under the cursor.
  // Closed via outside click / Escape / scroll (see effect below).
  const [contextMenu, setContextMenu] = React.useState<{ x: number; y: number; listing: Listing } | null>(null)

  // Bulk-select state for the listings table. `lastSelectedId` powers
  // shift-click range selection (Excel-style) — the next shift+click extends
  // selection from the last-toggled row to the new row.
  const [selectedIds, setSelectedIds] = React.useState<Set<number>>(new Set())
  const [lastSelectedId, setLastSelectedId] = React.useState<number | null>(null)
  const [bulkBusy, setBulkBusy] = React.useState(false)

  // Add Profile form state
  const [isAddingProfile, setIsAddingProfile] = React.useState(false)
  const [newProfile, setNewProfile] = React.useState({
    url: "",
    category: "",
    name: "",
  })
  const [submittingProfile, setSubmittingProfile] = React.useState(false)

  // API Call Wrapper
  const api = React.useCallback(async (path: string, opts?: RequestInit) => {
    const r = await fetch(path, opts)
    if (!r.ok) {
      const e = await r.json().catch(() => ({ error: r.statusText }))
      throw new Error(e.error || r.statusText)
    }
    return r.status === 204 ? null : r.json()
  }, [])

  // Lazy-load the sent_message history when the user opens a listing.
  React.useEffect(() => {
    if (!selectedListing) {
      setListingMessages([])
      return
    }
    let cancelled = false
    setLoadingMessages(true)
    api(`/api/listings/${selectedListing.id}/messages`)
      .then((rows: SentMessage[]) => {
        if (!cancelled) setListingMessages(rows ?? [])
      })
      .catch(() => {
        if (!cancelled) setListingMessages([])
      })
      .finally(() => {
        if (!cancelled) setLoadingMessages(false)
      })
    return () => {
      cancelled = true
    }
  }, [selectedListing, api])

  // Load Data
  const loadData = React.useCallback(async (showIndicator = false) => {
    if (showIndicator) setRefreshing(true)
    try {
      const oData = await api("/api/overview")
      setOverview(oData)
      
      const pData = await api("/api/profiles")
      setProfiles(pData || [])
      
      const lData = await api("/api/listings?limit=100")
      setListings(lData || [])

      const inData = await api(`/api/inbox?limit=100${inboxLandlordOnly ? "&landlord=1" : ""}`)
      setInbox(inData || [])

      const qData: QueueData = await api("/api/queue")
      setQueue({ pending: qData?.pending ?? null, next: qData?.next ?? [] })

      const cData: CampaignCfg[] = (await api("/api/campaigns")) || []
      setCampaigns(cData)

      const ckData: CookieInfo = await api("/api/cookie")
      setCookieInfo(ckData)

      const emData: EmailConfig = await api("/api/email")
      setEmailInfo(emData)
      // Only seed the draft from the server while the user hasn't started
      // typing — same pattern as campaign drafts. Password stays empty.
      setEmailTouched(touched => {
        if (!touched) {
          setEmailDraft(prev => ({
            ...prev,
            imap_host: emData.imap_host,
            username: emData.username,
            mailbox: emData.mailbox || "INBOX",
            lookback_hours: emData.lookback_hours || 72,
            senders: emData.senders,
          }))
        }
        return touched
      })
      // Only seed drafts the user hasn't started editing, so background polling
      // doesn't clobber in-progress edits.
      setDrafts(prev => {
        const next = { ...prev }
        for (const c of cData) {
          if (!next[c.name]) next[c.name] = { ai_prompt: c.ai_prompt, template: c.template }
        }
        return next
      })

      setError(null)
    } catch (e: unknown) {
      const message = errorMessage(e, "Fehler beim Laden der Daten")
      setError(message)
      toast.error("Verbindungsfehler", {
        description: message || "Daten konnten nicht aktualisiert werden.",
      })
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [api])

  // Initial and Interval polling
  React.useEffect(() => {
    const initial = window.setTimeout(() => {
      void loadData()
    }, 0)
    const interval = setInterval(() => loadData(true), 10000)
    return () => {
      window.clearTimeout(initial)
      clearInterval(interval)
    }
  }, [loadData])

  // Refetch only the inbox when the landlord-only toggle changes. Skips the
  // first run (loadData already fetched it) by gating on inboxLandlordOnly's
  // truth value — the default is false, same as the initial loadData query.
  const refreshInbox = React.useCallback(async () => {
    try {
      const inData = await api(`/api/inbox?limit=100${inboxLandlordOnly ? "&landlord=1" : ""}`)
      setInbox(inData || [])
    } catch {
      // The next 10s loadData tick will retry; surfacing a toast here would be noisy.
    }
  }, [api, inboxLandlordOnly])

  React.useEffect(() => {
    void refreshInbox()
  }, [refreshInbox])

  // Manual mailbox poll trigger: POST /api/inbox/scan, then refresh the table.
  // The response carries per-mail counts and any per-message errors so the UI
  // can show what actually happened instead of a bare "ok".
  const triggerInboxScan = React.useCallback(async () => {
    setInboxScanning(true)
    setInboxScanError(null)
    try {
      const res = await api("/api/inbox/scan", { method: "POST" }) as InboxScanResult
      setInboxScanResult(res)
      if (res.landlord_replies > 0) {
        toast.success(`Posteingang gescannt: ${res.landlord_replies} Vermieter-Antwort${res.landlord_replies === 1 ? "" : "en"}`)
      } else if (res.fetched > 0) {
        toast.success(`Posteingang gescannt: ${res.fetched} neue IS24-Mail${res.fetched === 1 ? "" : "s"} (System / Info)`)
      } else if (res.inspected > 0) {
        toast.warning(`${res.inspected} Mail${res.inspected === 1 ? "" : "s"} im Zeitfenster — keine vom IS24-Filter`)
      } else {
        toast.success("Posteingang gescannt — keine neuen Mails")
      }
      await refreshInbox()
    } catch (e: unknown) {
      const msg = errorMessage(e, "Unbekannter Fehler")
      setInboxScanError(msg)
      setInboxScanResult(null)
      toast.error("Scan fehlgeschlagen", { description: msg })
    } finally {
      setInboxScanning(false)
    }
  }, [api, refreshInbox])

  // Close the row context menu on outside click, Escape, or scroll. Inside
  // clicks land on the menu's own onClick handlers before this fires.
  React.useEffect(() => {
    if (!contextMenu) return
    const close = () => setContextMenu(null)
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") close() }
    window.addEventListener("mousedown", close)
    window.addEventListener("keydown", onKey)
    window.addEventListener("scroll", close, true)
    return () => {
      window.removeEventListener("mousedown", close)
      window.removeEventListener("keydown", onKey)
      window.removeEventListener("scroll", close, true)
    }
  }, [contextMenu])

  // Resolve the pending approval card via the scheduler (same code path as
  // the Telegram ✅/❌ buttons). Refreshes the queue so the next eligible
  // listing becomes the new pending entry — or shows the empty state.
  const decidePending = async (action: "approve" | "reject") => {
    if (!queue.pending) return
    setQueueBusy(action)
    try {
      await api(`/api/queue/${action}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ sent_message_id: queue.pending.sent_message_id }),
      })
      toast.success(action === "approve" ? "Nachricht wird gesendet" : "Vorschlag verworfen", {
        description: action === "approve"
          ? "Der Bot reicht das Kontaktformular jetzt ein."
          : "Die Wohnung kommt für 6 Stunden in den Reject-Cooldown.",
      })
      await loadData(true)
    } catch (e: unknown) {
      toast.error("Fehler", {
        description: errorMessage(e, action === "approve"
          ? "Bestätigung konnte nicht verarbeitet werden."
          : "Verwerfen konnte nicht verarbeitet werden."),
      })
    } finally {
      setQueueBusy(null)
    }
  }

  // Set Settings (Auto Contact Mode / Quiet Hours / Timing)
  const setSetting = async (body: {
    contact_mode?: string
    quiet_hours?: boolean
    quiet_hours_start?: string
    quiet_hours_end?: string
    poll_interval_seconds?: number
    contact_type_delay_ms?: number
    contact_action_delay_ms?: number
    exclude_furnished?: boolean
  }) => {
    try {
      const oData = await api("/api/settings", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      })
      setOverview(oData)
      toast.success("Einstellungen gespeichert", {
        description: "Änderungen wurden erfolgreich übernommen.",
      })
    } catch (e: unknown) {
      toast.error("Fehler", {
        description: errorMessage(e, "Einstellungen konnten nicht gespeichert werden."),
      })
    }
  }

  // Save a campaign's AI prompt + message template override.
  const saveCampaign = async (name: string) => {
    const d = drafts[name]
    if (!d) return
    setSavingCampaign(name)
    try {
      const updated: CampaignCfg = await api(`/api/campaigns/${encodeURIComponent(name)}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ ai_prompt: d.ai_prompt, template: d.template }),
      })
      setCampaigns(prev => prev.map(c => (c.name === name ? updated : c)))
      setDrafts(prev => ({ ...prev, [name]: { ai_prompt: updated.ai_prompt, template: updated.template } }))
      toast.success("Kampagne gespeichert", {
        description: `"${name}" wird ab dem nächsten Suchzyklus verwendet.`,
      })
    } catch (e: unknown) {
      toast.error("Speichern fehlgeschlagen", { description: errorMessage(e, "Kampagne konnte nicht gespeichert werden.") })
    } finally {
      setSavingCampaign(null)
    }
  }

  // Save the IS24 cookie (hot reload + persistence backend-side).
  const saveCookie = async () => {
    const v = cookieDraft.trim()
    if (v.length < 50) {
      toast.error("Cookie zu kurz", { description: "Bitte den vollständigen Cookie-Header einfügen." })
      return
    }
    setSavingCookie(true)
    try {
      const updated: CookieInfo = await api("/api/cookie", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ cookie: v }),
      })
      setCookieInfo(updated)
      setCookieDraft("")
      toast.success("Cookie aktualisiert", {
        description: "Nächster Poll-Zyklus nutzt den neuen Cookie. Kein Restart nötig.",
      })
    } catch (e: unknown) {
      toast.error("Speichern fehlgeschlagen", { description: errorMessage(e, "Cookie konnte nicht gespeichert werden.") })
    } finally {
      setSavingCookie(false)
    }
  }

  // Save email (IMAP) settings. Password is only sent when the user typed a new
  // one; the API encrypts it before writing to the database.
  const saveEmail = async (enabled: boolean) => {
    setSavingEmail(true)
    try {
      const body: Record<string, unknown> = {
        enabled,
        imap_host: emailDraft.imap_host.trim(),
        username: emailDraft.username.trim(),
        mailbox: emailDraft.mailbox.trim() || "INBOX",
        lookback_hours: Number(emailDraft.lookback_hours) || 72,
        senders: emailDraft.senders,
      }
      const pwd = emailDraft.password.trim()
      if (pwd) body.password = pwd
      const updated: EmailConfig = await api("/api/email", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      })
      setEmailInfo(updated)
      setEmailDraft(prev => ({ ...prev, password: "" }))
      setEmailTouched(false)
      toast.success("E-Mail-Einstellungen gespeichert", {
        description: "Greifen beim nächsten Container-Restart.",
      })
    } catch (e: unknown) {
      toast.error("Speichern fehlgeschlagen", { description: errorMessage(e, "E-Mail-Einstellungen konnten nicht gespeichert werden.") })
    } finally {
      setSavingEmail(false)
    }
  }

  // Reset a campaign back to its config.yaml default (clears the override).
  const resetCampaign = async (name: string) => {
    if (!confirm(`Kampagne "${name}" auf den Standard aus config.yaml zurücksetzen?`)) return
    setSavingCampaign(name)
    try {
      const updated: CampaignCfg = await api(`/api/campaigns/${encodeURIComponent(name)}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ ai_prompt: "", template: "" }),
      })
      setCampaigns(prev => prev.map(c => (c.name === name ? updated : c)))
      setDrafts(prev => ({ ...prev, [name]: { ai_prompt: updated.ai_prompt, template: updated.template } }))
      toast.success("Auf Standard zurückgesetzt")
    } catch (e: unknown) {
      toast.error("Zurücksetzen fehlgeschlagen", { description: errorMessage(e, "Kampagne konnte nicht zurückgesetzt werden.") })
    } finally {
      setSavingCampaign(null)
    }
  }

  // Toggle Profile Active Status
  const toggleProfile = async (id: number, active: boolean) => {
    try {
      await api(`/api/profiles/${id}/active`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ active }),
      })
      
      // Update local state instantly for responsiveness
      setProfiles(prev =>
        prev.map(p => (p.id === id ? { ...p, active } : p))
      )
      
      toast.success(active ? "Profil aktiviert" : "Profil pausiert")
    } catch (e: unknown) {
      toast.error("Fehler beim Umschalten", {
        description: errorMessage(e, "Profilstatus konnte nicht geändert werden."),
      })
    }
  }

  // Clear the multi-select. Hoisted so handlers below can reset state after a
  // bulk action completes.
  const clearSelection = React.useCallback(() => {
    setSelectedIds(new Set())
    setLastSelectedId(null)
  }, [])

  // Bulk-apply the skip flag to every currently selected listing. One round
  // trip via POST /api/listings/skip; on success we patch local state without
  // re-fetching so the UI stays responsive.
  const bulkSetSkipped = async (skipped: boolean) => {
    const ids = Array.from(selectedIds)
    if (ids.length === 0) return
    setBulkBusy(true)
    try {
      const result: { updated: number } = await api("/api/listings/skip", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ ids, skipped }),
      })
      const idSet = new Set(ids)
      setListings(prev => prev.map(l => (idSet.has(l.id) ? { ...l, skipped } : l)))
      toast.success(
        `${result.updated} ${result.updated === 1 ? "Wohnung" : "Wohnungen"} ${skipped ? "ignoriert" : "wieder aufgenommen"}`
      )
      clearSelection()
    } catch (e: unknown) {
      toast.error("Aktion fehlgeschlagen", {
        description: errorMessage(e, "Konnte den Status nicht ändern."),
      })
    } finally {
      setBulkBusy(false)
    }
  }

  // Toggle a listing's manual ignore flag (right-click → "Ignorieren"). The
  // scheduler excludes skipped listings from auto-contact; they stay visible
  // in the dashboard with an "ignoriert" badge so the user can un-skip later.
  const toggleSkip = async (id: number, skipped: boolean) => {
    try {
      await api(`/api/listings/${id}/skip`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ skipped }),
      })
      setListings(prev => prev.map(l => (l.id === id ? { ...l, skipped } : l)))
      if (selectedListing?.id === id) {
        setSelectedListing(prev => (prev ? { ...prev, skipped } : prev))
      }
      toast.success(skipped ? "Als ignoriert markiert" : "Wieder aufgenommen")
    } catch (e: unknown) {
      toast.error("Fehler beim Markieren", {
        description: errorMessage(e, "Status konnte nicht geändert werden."),
      })
    }
  }

  // Undo a prior rejection so the listing flows through the approval queue
  // again. Counterpart to the Telegram ❌ — the user accidentally rejected and
  // wants it back.
  const undoReject = async (id: number) => {
    try {
      await api(`/api/listings/${id}/unreject`, { method: "POST" })
      setListings(prev => prev.map(l => (l.id === id ? { ...l, rejected: false } : l)))
      if (selectedListing?.id === id) {
        setSelectedListing(prev => (prev ? { ...prev, rejected: false } : prev))
      }
      toast.success("Verwerfung rückgängig gemacht")
    } catch (e: unknown) {
      toast.error("Fehler", {
        description: errorMessage(e, "Verwerfung konnte nicht rückgängig gemacht werden."),
      })
    }
  }

  // Delete Search Profile
  const deleteProfile = async (id: number) => {
    if (!confirm(`Suchprofil #${id} wirklich löschen?`)) return
    try {
      await api(`/api/profiles/${id}`, { method: "DELETE" })
      setProfiles(prev => prev.filter(p => p.id !== id))
      toast.success("Profil gelöscht")
    } catch (e: unknown) {
      toast.error("Löschen fehlgeschlagen", {
        description: errorMessage(e, "Profil konnte nicht gelöscht werden."),
      })
    }
  }

  // Add Search Profile
  const handleAddProfile = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!newProfile.url.trim()) {
      toast.error("Fehler", { description: "Die IS24-Such-URL darf nicht leer sein." })
      return
    }
    
    setSubmittingProfile(true)
    const category = newProfile.category || overview?.default_campaign || overview?.campaigns[0] || ""
    try {
      await api("/api/profiles", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          url: newProfile.url.trim(),
          category: category || undefined,
          name: newProfile.name.trim() || undefined,
        }),
      })
      
      // Reset and close dialog
      setNewProfile({ url: "", category: "", name: "" })
      setIsAddingProfile(false)
      toast.success("Suchprofil erstellt", {
        description: "Das Profil wird ab dem nächsten Suchzyklus abgefragt.",
      })
      
      // Reload lists
      await loadData()
    } catch (e: unknown) {
      toast.error("Erstellen fehlgeschlagen", {
        description: errorMessage(e, "Profil konnte nicht erstellt werden."),
      })
    } finally {
      setSubmittingProfile(false)
    }
  }

  // Format Helper
  const formatDate = (dateStr?: string) => {
    if (!dateStr) return "–"
    try {
      const d = new Date(dateStr)
      return d.toLocaleDateString("de-DE", {
        day: "2-digit",
        month: "2-digit",
        year: "numeric",
      }) + " " + d.toLocaleTimeString("de-DE", {
        hour: "2-digit",
        minute: "2-digit",
      })
    } catch {
      return dateStr
    }
  }

  // Filter listings by search profile and free-text query.
  const filteredListings = React.useMemo(() => {
    const q = searchQuery.trim().toLowerCase()
    return listings.filter(l => {
      if (profileFilter !== "all" && String(l.search_profile_id ?? "") !== profileFilter) {
        return false
      }
      if (!q) return true
      return (
        l.title.toLowerCase().includes(q) ||
        (l.address && l.address.toLowerCase().includes(q)) ||
        (l.city && l.city.toLowerCase().includes(q)) ||
        (l.campaign && l.campaign.toLowerCase().includes(q))
      )
    })
  }, [listings, searchQuery, profileFilter])

  // Selection helpers — depend on filteredListings so range-selection and
  // select-all both honour the active filter (selecting "all" only flips the
  // currently visible rows, not the entire backing list).
  const toggleRowSelected = (id: number, shiftKey: boolean) => {
    setSelectedIds(prev => {
      const next = new Set(prev)
      if (shiftKey && lastSelectedId != null && lastSelectedId !== id) {
        const visibleIds = filteredListings.map(l => l.id)
        const a = visibleIds.indexOf(lastSelectedId)
        const b = visibleIds.indexOf(id)
        if (a >= 0 && b >= 0) {
          const [from, to] = a < b ? [a, b] : [b, a]
          // Decide whether the range op adds or removes based on the target
          // row's current state — matches the user's intent ("extend selection
          // to here" vs "extend deselection to here").
          const add = !prev.has(id)
          for (let i = from; i <= to; i++) {
            if (add) next.add(visibleIds[i])
            else next.delete(visibleIds[i])
          }
          return next
        }
      }
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
    setLastSelectedId(id)
  }
  const allVisibleSelected =
    filteredListings.length > 0 && filteredListings.every(l => selectedIds.has(l.id))
  const someVisibleSelected =
    !allVisibleSelected && filteredListings.some(l => selectedIds.has(l.id))
  const toggleSelectAllVisible = () => {
    setSelectedIds(prev => {
      const next = new Set(prev)
      if (allVisibleSelected) {
        for (const l of filteredListings) next.delete(l.id)
      } else {
        for (const l of filteredListings) next.add(l.id)
      }
      return next
    })
  }

  if (loading && !overview) {
    return (
      <div className="flex h-screen w-screen flex-col items-center justify-center gap-4 bg-background text-foreground transition-colors duration-300">
        <Home className="h-12 w-12 text-primary animate-pulse" />
        <p className="text-sm font-medium animate-pulse">ImmoBot Dashboard lädt...</p>
      </div>
    )
  }

  const currentOverview = overview ?? EMPTY_OVERVIEW
  const selectedProfileCategory = newProfile.category || currentOverview.default_campaign || currentOverview.campaigns[0] || ""

  return (
    <div className="min-h-screen bg-background text-foreground transition-colors duration-300 antialiased font-sans flex">
      <Toaster position="bottom-right" />

      {/* Right-click context menu for listing rows. Clamped to the viewport so
          opening near the right/bottom edge doesn't push it off-screen. When
          right-clicking on (or after auto-selecting) a row that's part of a
          multi-row selection, the actions apply to the whole batch via the
          bulk endpoint. */}
      {contextMenu && (() => {
        const inMulti = selectedIds.has(contextMenu.listing.id) && selectedIds.size > 1
        const count = inMulti ? selectedIds.size : 1
        // The skip-toggle label flips on the right-clicked row's own state —
        // even in bulk mode — so users get a predictable verb. The bulk call
        // sets ALL selected rows to the target state regardless of their
        // previous individual states.
        const targetSkipped = !contextMenu.listing.skipped
        return (
        <div
          className="fixed z-50 min-w-[220px] rounded-md border bg-popover text-popover-foreground shadow-md py-1 text-sm"
          style={{
            left: Math.min(contextMenu.x, (typeof window !== "undefined" ? window.innerWidth : 1024) - 240),
            top: Math.min(contextMenu.y, (typeof window !== "undefined" ? window.innerHeight : 768) - 120),
          }}
          onMouseDown={(e) => e.stopPropagation()}
          onContextMenu={(e) => e.preventDefault()}
        >
          {inMulti && (
            <div className="px-3 py-1.5 text-[10px] uppercase tracking-wider text-muted-foreground border-b">
              {count} ausgewählt
            </div>
          )}
          <button
            type="button"
            className="w-full flex items-center gap-2 px-3 py-2 text-left hover:bg-muted/60 transition-colors"
            onClick={() => {
              const l = contextMenu.listing
              setContextMenu(null)
              if (inMulti) {
                void bulkSetSkipped(targetSkipped)
              } else {
                void toggleSkip(l.id, targetSkipped)
              }
            }}
          >
            {targetSkipped ? (
              <><EyeOff className="h-4 w-4 shrink-0" /> Ignorieren{inMulti ? ` (${count})` : ""}</>
            ) : (
              <><RotateCcw className="h-4 w-4 shrink-0" /> Wieder aufnehmen{inMulti ? ` (${count})` : ""}</>
            )}
          </button>
          {!inMulti && contextMenu.listing.rejected && (
            <button
              type="button"
              className="w-full flex items-center gap-2 px-3 py-2 text-left hover:bg-muted/60 transition-colors"
              onClick={() => {
                const l = contextMenu.listing
                setContextMenu(null)
                void undoReject(l.id)
              }}
            >
              <RotateCcw className="h-4 w-4 shrink-0" /> Verwerfung rückgängig
            </button>
          )}
          {!inMulti && (
            <button
              type="button"
              className="w-full flex items-center gap-2 px-3 py-2 text-left hover:bg-muted/60 transition-colors"
              onClick={() => {
                window.open(contextMenu.listing.url, "_blank", "noreferrer")
                setContextMenu(null)
              }}
            >
              <ExternalLink className="h-4 w-4 shrink-0" /> Auf IS24 öffnen
            </button>
          )}
        </div>
        )
      })()}

      {/* Sidebar */}
      <aside className="hidden md:flex md:w-56 lg:w-60 shrink-0 flex-col border-r bg-muted/10 sticky top-0 h-screen">
        <div className="px-5 pt-5 pb-3 border-b">
          <h1 className="text-lg font-bold tracking-tight">svensk</h1>
          <p className="text-[10px] text-muted-foreground mt-0.5">ImmoBot Dashboard</p>
        </div>

        <nav className="flex-1 px-2 py-3 space-y-1">
          {VIEWS.map(v => {
            const Icon = v.icon
            const active = view === v.key
            return (
              <button
                key={v.key}
                onClick={() => setView(v.key)}
                className={`w-full flex items-center gap-2.5 rounded-md px-3 py-2 text-sm font-medium transition-colors ${
                  active
                    ? "bg-foreground text-background shadow-sm"
                    : "text-muted-foreground hover:bg-muted/50 hover:text-foreground"
                }`}
              >
                <Icon className="h-4 w-4 shrink-0" />
                {v.label}
              </button>
            )
          })}
        </nav>

        {/* Sidebar footer: status + last poll + theme/refresh */}
        <div className="px-3 py-3 border-t space-y-3">
          <div
            className={`flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-[11px] font-semibold ${
              currentOverview.contact_mode === "on"
                ? STATUS_TONE.active
                : currentOverview.contact_mode === "test" || currentOverview.contact_mode === "approve"
                ? STATUS_TONE.medium
                : STATUS_TONE.quiet
            }`}
            title={currentOverview.contact_label}
          >
            <span
              className={`h-1.5 w-1.5 rounded-full ${
                currentOverview.contact_mode === "on"
                  ? "bg-background animate-ping dark:bg-background"
                  : currentOverview.contact_mode === "test" || currentOverview.contact_mode === "approve"
                  ? "bg-foreground animate-pulse"
                  : "bg-muted-foreground"
              }`}
            />
            <span className="truncate">{currentOverview.contact_label}</span>
          </div>
          <div className="text-[10px] text-muted-foreground px-1">
            {currentOverview.last_poll ? (
              <>
                letzter Poll
                <br />
                <span className="font-semibold text-foreground">
                  {formatDate(currentOverview.last_poll)}
                </span>
              </>
            ) : (
              "noch kein Poll"
            )}
          </div>
          <div className="flex items-center gap-2">
            <Button
              variant="ghost"
              size="icon"
              className="h-8 w-8"
              onClick={() => setTheme(resolvedTheme === "dark" ? "light" : "dark")}
              title="Design umschalten"
            >
              {resolvedTheme === "dark" ? (
                <Sun className="h-4 w-4 text-foreground" />
              ) : (
                <Moon className="h-4 w-4 text-primary" />
              )}
            </Button>
            <Button
              variant="outline"
              size="icon"
              className={`h-8 w-8 transition-transform duration-300 ${refreshing ? "rotate-180" : ""}`}
              onClick={() => loadData(true)}
              disabled={refreshing}
              title="Aktualisieren"
            >
              <RefreshCw className={`h-4 w-4 ${refreshing ? "animate-spin" : ""}`} />
            </Button>
          </div>
        </div>
      </aside>

      {/* Mobile top bar with nav select + actions */}
      <div className="md:hidden fixed top-0 inset-x-0 z-40 border-b bg-background/95 backdrop-blur-md">
        <div className="flex items-center justify-between gap-2 px-4 py-2">
          <h1 className="text-base font-bold tracking-tight">svensk</h1>
          <Select value={view} onValueChange={(v) => setView(v as View)}>
            <SelectTrigger className="h-8 w-[170px] text-xs"><SelectValue /></SelectTrigger>
            <SelectContent>
              {VIEWS.map(v => (
                <SelectItem key={v.key} value={v.key}>{v.label}</SelectItem>
              ))}
            </SelectContent>
          </Select>
          <div className="flex items-center gap-1">
            <Button variant="ghost" size="icon" className="h-8 w-8"
              onClick={() => setTheme(resolvedTheme === "dark" ? "light" : "dark")}>
              {resolvedTheme === "dark" ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
            </Button>
            <Button variant="outline" size="icon" className="h-8 w-8"
              onClick={() => loadData(true)} disabled={refreshing}>
              <RefreshCw className={`h-4 w-4 ${refreshing ? "animate-spin" : ""}`} />
            </Button>
          </div>
        </div>
      </div>

      {/* Main content area — only the active section is rendered. */}
      <main className="flex-1 min-w-0 p-4 sm:p-6 md:p-8 space-y-6 mt-12 md:mt-0">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <h2 className="text-xl font-bold tracking-tight">
              {VIEWS.find(v => v.key === view)?.label}
            </h2>
            <p className="text-xs text-muted-foreground mt-0.5">
              {sectionSubtitle(view, currentOverview)}
            </p>
          </div>
        </div>

        {error && (
          <div className="flex flex-col gap-3 rounded-lg border border-border bg-muted/40 p-4 text-sm text-foreground sm:flex-row sm:items-center">
            <ShieldAlert className="h-5 w-5 shrink-0" />
            <div className="min-w-0 flex-1">
              <h5 className="font-semibold">Verbindungsfehler zum Backend</h5>
              <p className="text-xs text-muted-foreground mt-0.5">{error}</p>
            </div>
            <Button
              variant="outline"
              size="sm"
              className="bg-transparent sm:ml-auto"
              onClick={() => loadData()}
            >
              Erneut versuchen
            </Button>
          </div>
        )}

        {/* Settings view: full-width settings card. */}
        {view === "settings" && (
          <Card className="shadow-sm border border-border/60 hover:border-border transition-all duration-300">
            <CardHeader className="pb-4">
              <CardTitle className="flex items-center gap-2 text-sm font-semibold uppercase tracking-wider text-muted-foreground">
                <Settings className="h-4 w-4" /> Einstellungen
              </CardTitle>
            </CardHeader>
            <CardContent className="space-y-6">
              {/* Contact Mode Select */}
              <div className="space-y-2">
                <label className="text-xs font-semibold text-muted-foreground">Kontakt-Modus</label>
                <div className="flex w-full flex-col gap-3 rounded-lg border p-3 bg-muted/10 sm:flex-row sm:items-center sm:justify-between">
                  <span className="text-sm font-medium">Bewerbungen:</span>
                  <div className="grid grid-cols-2 sm:grid-cols-4 rounded-md border p-1 bg-muted/40 text-xs sm:inline-flex">
                    <Button
                      variant={currentOverview.contact_mode === "off" ? "default" : "ghost"}
                      size="sm"
                      onClick={() => setSetting({ contact_mode: "off" })}
                      className="h-7 gap-1 px-3 text-xs font-medium rounded"
                    >
                      <Pause className="h-3 w-3" /> Aus
                    </Button>
                    <Button
                      variant={currentOverview.contact_mode === "test" ? "default" : "ghost"}
                      size="sm"
                      onClick={() => setSetting({ contact_mode: "test" })}
                      className="h-7 gap-1 px-3 text-xs font-medium rounded"
                    >
                      <Eye className="h-3 w-3" /> Test
                    </Button>
                    <Button
                      variant={currentOverview.contact_mode === "approve" ? "default" : "ghost"}
                      size="sm"
                      onClick={() => setSetting({ contact_mode: "approve" })}
                      className="h-7 gap-1 px-3 text-xs font-medium rounded"
                    >
                      <CheckCheck className="h-3 w-3" /> Approval
                    </Button>
                    <Button
                      variant={currentOverview.contact_mode === "on" ? "default" : "ghost"}
                      size="sm"
                      onClick={() => setSetting({ contact_mode: "on" })}
                      className={`h-7 gap-1 px-3 text-xs font-medium rounded transition-colors ${
                        currentOverview.contact_mode === "on"
                          ? "bg-foreground hover:bg-foreground text-background font-semibold shadow-sm"
                          : ""
                      }`}
                    >
                      <Play className="h-3 w-3" /> Live
                    </Button>
                  </div>
                </div>
              </div>

              {/* Quiet Hours Switch + Window */}
              <div className="space-y-2 rounded-lg border p-3 bg-muted/10">
                <div className="flex items-center justify-between">
                  <div className="flex flex-col gap-0.5">
                    <span className="text-sm font-semibold">Ruhezeiten aktivieren</span>
                    <span className="text-xs text-muted-foreground">Keine automatischen Bewerbungen nachts</span>
                  </div>
                  <div className="flex items-center gap-3">
                    <div className="flex h-8 w-8 items-center justify-center rounded-full bg-muted/30">
                      {currentOverview.quiet_hours ? (
                        <Moon className="h-4 w-4 text-foreground" />
                      ) : (
                        <Sun className="h-4 w-4 text-muted-foreground" />
                      )}
                    </div>
                    <Switch
                      checked={currentOverview.quiet_hours}
                      onCheckedChange={(checked) => setSetting({ quiet_hours: checked })}
                    />
                  </div>
                </div>
                {/* Window pickers — onBlur saves so users can tab between fields without intermediate POSTs. */}
                <div className="flex items-center gap-2 pt-2 border-t border-border/40">
                  <span className="text-xs text-muted-foreground">Fenster:</span>
                  <Input
                    type="time"
                    value={currentOverview.quiet_hours_start}
                    onChange={(e) =>
                      setOverview(prev => prev ? { ...prev, quiet_hours_start: e.target.value } : prev)
                    }
                    onBlur={(e) => setSetting({ quiet_hours_start: e.target.value })}
                    className="h-8 w-[110px] font-mono text-xs"
                  />
                  <span className="text-xs text-muted-foreground">–</span>
                  <Input
                    type="time"
                    value={currentOverview.quiet_hours_end}
                    onChange={(e) =>
                      setOverview(prev => prev ? { ...prev, quiet_hours_end: e.target.value } : prev)
                    }
                    onBlur={(e) => setSetting({ quiet_hours_end: e.target.value })}
                    className="h-8 w-[110px] font-mono text-xs"
                  />
                </div>
              </div>

              {/* Timing: poll interval + contact form delays. Edits commit on
                  pointer release (onMouseUp/onTouchEnd) so dragging the slider
                  doesn't fire a POST per pixel. */}
              <div className="space-y-4 rounded-lg border p-3 bg-muted/10">
                <div className="flex flex-col gap-0.5">
                  <span className="text-sm font-semibold">Timing</span>
                  <span className="text-xs text-muted-foreground">
                    Wie schnell neue Wohnungen entdeckt und angeschrieben werden.
                  </span>
                </div>

                <TimingSlider
                  label="Poll-Intervall"
                  hint="Wartezeit zwischen IS24-Suchen. Kürzer = schneller entdeckt, aber höheres Sperrungsrisiko."
                  value={currentOverview.poll_interval_seconds}
                  min={currentOverview.timing_ranges.poll_interval_seconds.min}
                  max={currentOverview.timing_ranges.poll_interval_seconds.max}
                  step={30}
                  format={(s) => (s >= 60 ? `${Math.round(s / 60)} min` : `${s} s`)}
                  onPreview={(v) =>
                    setOverview(prev => prev ? { ...prev, poll_interval_seconds: v } : prev)
                  }
                  onCommit={(v) => setSetting({ poll_interval_seconds: v })}
                />

                <TimingSlider
                  label="Tipp-Verzögerung"
                  hint="Zeit zwischen den Buchstaben beim Tippen im Kontaktformular (Anti-Bot)."
                  value={currentOverview.contact_type_delay_ms}
                  min={currentOverview.timing_ranges.contact_type_delay_ms.min}
                  max={currentOverview.timing_ranges.contact_type_delay_ms.max}
                  step={10}
                  format={(ms) => `${ms} ms`}
                  onPreview={(v) =>
                    setOverview(prev => prev ? { ...prev, contact_type_delay_ms: v } : prev)
                  }
                  onCommit={(v) => setSetting({ contact_type_delay_ms: v })}
                />

                <TimingSlider
                  label="Aktions-Pause"
                  hint="Pause zwischen Klick/Feldwechsel im Kontaktformular."
                  value={currentOverview.contact_action_delay_ms}
                  min={currentOverview.timing_ranges.contact_action_delay_ms.min}
                  max={currentOverview.timing_ranges.contact_action_delay_ms.max}
                  step={100}
                  format={(ms) => (ms >= 1000 ? `${(ms / 1000).toFixed(1)} s` : `${ms} ms`)}
                  onPreview={(v) =>
                    setOverview(prev => prev ? { ...prev, contact_action_delay_ms: v } : prev)
                  }
                  onCommit={(v) => setSetting({ contact_action_delay_ms: v })}
                />
              </div>

              {/* Filter — global rules applied to every search profile. */}
              <div className="space-y-2 rounded-lg border p-3 bg-muted/10">
                <div className="flex items-center justify-between">
                  <div className="flex flex-col gap-0.5">
                    <span className="text-sm font-semibold">Möblierte Wohnungen ausschließen</span>
                    <span className="text-xs text-muted-foreground">
                      Listings mit „möbliert", „furnished" etc. in Titel/Beschreibung werden weder gemeldet noch angeschrieben.
                      Auf IS24 ist das nicht filterbar, der Bot prüft das selbst.
                    </span>
                  </div>
                  <Switch
                    checked={currentOverview.exclude_furnished}
                    onCheckedChange={(checked) => setSetting({ exclude_furnished: checked })}
                  />
                </div>
              </div>
            </CardContent>
          </Card>
        )}

        {/* Overview view: quick status + stats grid. */}
        {view === "overview" && (
        <>
          <Card className="shadow-sm border border-border/60">
            <CardContent className="pt-5 pb-5">
              <div className="flex flex-wrap items-center gap-x-6 gap-y-3">
                <div className="flex items-center gap-2">
                  <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Modus</span>
                  <Badge
                    variant="outline"
                    className={`text-xs px-2.5 py-1 ${
                      currentOverview.contact_mode === "on"
                        ? STATUS_TONE.active
                        : currentOverview.contact_mode === "test" || currentOverview.contact_mode === "approve"
                        ? STATUS_TONE.medium
                        : STATUS_TONE.quiet
                    }`}
                  >
                    {currentOverview.contact_label}
                  </Badge>
                </div>
                <div className="flex items-center gap-2">
                  <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Ruhezeit</span>
                  {currentOverview.quiet_hours ? (
                    <Badge variant="outline" className={`text-xs ${STATUS_TONE.medium}`}>
                      <Moon className="h-3 w-3" /> {currentOverview.quiet_hours_start}–{currentOverview.quiet_hours_end}
                    </Badge>
                  ) : (
                    <Badge variant="outline" className={`text-xs ${STATUS_TONE.subtle}`}>
                      <Sun className="h-3 w-3" /> 24/7
                    </Badge>
                  )}
                </div>
                <div className="flex items-center gap-2">
                  <span className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Cookie</span>
                  {cookieInfo?.source === "meta" ? (
                    <Badge variant="outline" className={`text-xs ${STATUS_TONE.active}`}>Override · {cookieInfo.length} Z.</Badge>
                  ) : cookieInfo?.source === "env" ? (
                    <Badge variant="outline" className={`text-xs ${STATUS_TONE.medium}`}>.env · {cookieInfo.length} Z.</Badge>
                  ) : (
                    <Badge variant="outline" className={`text-xs ${STATUS_TONE.quiet}`}>nicht gesetzt</Badge>
                  )}
                </div>
                <div className="flex items-center gap-2 ml-auto">
                  <Button variant="ghost" size="sm" className="h-7 text-xs" onClick={() => setView("settings")}>
                    Einstellungen
                  </Button>
                  <Button variant="ghost" size="sm" className="h-7 text-xs" onClick={() => setView("profiles")}>
                    Suchprofile
                  </Button>
                </div>
              </div>
            </CardContent>
          </Card>

          <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
            {/* Stat Total */}
            <Card className="flex flex-col justify-between overflow-hidden shadow-sm border border-border/60 hover:border-border transition-all duration-300">
              <CardHeader className="pb-2">
                <CardDescription className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Gefunden</CardDescription>
              </CardHeader>
              <CardContent className="pb-6">
                <span className="text-4xl font-extrabold tracking-tight">{currentOverview.stats.total}</span>
              </CardContent>
              <div className="h-1.5 w-full bg-primary/10" />
            </Card>

            {/* Stat Notified */}
            <Card className="flex flex-col justify-between overflow-hidden shadow-sm border border-border/60 hover:border-border transition-all duration-300">
              <CardHeader className="pb-2">
                <CardDescription className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Notifiziert</CardDescription>
              </CardHeader>
              <CardContent className="pb-6">
                <span className="text-4xl font-extrabold tracking-tight text-foreground">{currentOverview.stats.notified}</span>
              </CardContent>
              <div className="h-1.5 w-full bg-muted" />
            </Card>

            {/* Stat Contacted */}
            <Card className="flex flex-col justify-between overflow-hidden shadow-sm border border-border/60 hover:border-border transition-all duration-300">
              <CardHeader className="pb-2">
                <CardDescription className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">Kontaktiert</CardDescription>
              </CardHeader>
              <CardContent className="pb-6">
                <span className="text-4xl font-extrabold tracking-tight text-foreground">{currentOverview.stats.contacted}</span>
              </CardContent>
              <div className="h-1.5 w-full bg-foreground/20" />
            </Card>
          </div>
        </>
        )}

        {/* Cookie card — part of Einstellungen. */}
        {view === "settings" && (
        <Card className="shadow-sm border border-border/60">
          <CardHeader className="pb-3">
            <div className="flex items-center justify-between">
              <div>
                <CardTitle className="text-md font-bold tracking-tight flex items-center gap-2">
                  <CookieIcon className="h-4 w-4" /> IS24-Cookie
                </CardTitle>
                <CardDescription className="text-xs">
                  Aktualisieren ohne Restart. Cookie aus DevTools → www.immobilienscout24.de → alle als <code className="rounded bg-muted px-1 py-0.5 text-[10px]">name=val; name=val</code> kopieren.
                </CardDescription>
              </div>
              {cookieInfo && (
                <Badge
                  variant="outline"
                  className={`text-[10px] ${
                    cookieInfo.source === "meta"
                      ? STATUS_TONE.active
                      : cookieInfo.source === "env"
                      ? STATUS_TONE.medium
                      : STATUS_TONE.quiet
                  }`}
                >
                  {cookieInfo.source === "meta"
                    ? `Override aktiv (${cookieInfo.length} Z.)`
                    : cookieInfo.source === "env"
                    ? `Aus .env (${cookieInfo.length} Z.)`
                    : "Kein Cookie gesetzt"}
                </Badge>
              )}
            </div>
          </CardHeader>
          <CardContent>
            <div className="flex flex-col gap-2 sm:flex-row">
              <textarea
                rows={3}
                value={cookieDraft}
                onChange={(e) => setCookieDraft(e.target.value)}
                placeholder="name1=value1; name2=value2; …"
                className="flex-1 rounded-md border border-input bg-transparent px-3 py-2 text-xs font-mono shadow-sm placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring resize-y"
              />
              <Button
                onClick={saveCookie}
                disabled={savingCookie || cookieDraft.trim().length < 50}
                className="h-10 px-4 font-semibold sm:h-auto sm:self-stretch"
              >
                {savingCookie ? "Speichert…" : "Übernehmen"}
              </Button>
            </div>
          </CardContent>
        </Card>
        )}

        {/* Email (IMAP) configuration card — part of Einstellungen. */}
        {view === "settings" && (
        <Card className="shadow-sm border border-border/60">
          <CardHeader className="pb-3">
            <div className="flex items-center justify-between">
              <div>
                <CardTitle className="text-md font-bold tracking-tight flex items-center gap-2">
                  <Mail className="h-4 w-4" /> E-Mail (IMAP)
                </CardTitle>
                <CardDescription className="text-xs">
                  Posteingang nach IS24-Anbieter-Antworten überwachen. Änderungen werden beim nächsten
                  Container-Restart aktiv. Bei Gmail/IONOS: ein App-Passwort verwenden.
                </CardDescription>
              </div>
              {emailInfo && (
                <Badge
                  variant="outline"
                  className={`text-[10px] ${
                    emailInfo.enabled
                      ? STATUS_TONE.active
                      : emailInfo.meta_override
                      ? STATUS_TONE.medium
                      : STATUS_TONE.quiet
                  }`}
                >
                  {emailInfo.enabled
                    ? "aktiv"
                    : emailInfo.meta_override
                    ? "konfiguriert"
                    : "deaktiviert"}
                </Badge>
              )}
            </div>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="flex items-center justify-between rounded-lg border p-3 bg-muted/10">
              <div className="flex flex-col gap-0.5">
                <span className="text-sm font-semibold">IMAP-Monitor aktivieren</span>
                <span className="text-xs text-muted-foreground">
                  {emailInfo?.restart_required
                    ? "Gespeichert — wird beim nächsten Restart geladen."
                    : "OpenAI muss aktiv sein (für die Klassifizierung der Mails)."}
                </span>
              </div>
              <Switch
                checked={emailInfo?.enabled ?? false}
                onCheckedChange={(checked) => saveEmail(checked)}
                disabled={savingEmail}
              />
            </div>

            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <div className="space-y-1.5">
                <label className="text-xs font-bold text-muted-foreground uppercase">IMAP-Host</label>
                <Input
                  placeholder="imap.gmail.com:993"
                  value={emailDraft.imap_host}
                  onChange={(e) => { setEmailTouched(true); setEmailDraft(prev => ({ ...prev, imap_host: e.target.value })) }}
                  className="font-mono text-xs"
                />
                <p className="text-[10px] text-muted-foreground">host:port, implizites TLS.</p>
              </div>
              <div className="space-y-1.5">
                <label className="text-xs font-bold text-muted-foreground uppercase">Postfach</label>
                <Input
                  placeholder="INBOX"
                  value={emailDraft.mailbox}
                  onChange={(e) => { setEmailTouched(true); setEmailDraft(prev => ({ ...prev, mailbox: e.target.value })) }}
                  className="font-mono text-xs"
                />
              </div>
              <div className="space-y-1.5">
                <label className="text-xs font-bold text-muted-foreground uppercase">Benutzername</label>
                <Input
                  placeholder="bot@example.com"
                  value={emailDraft.username}
                  onChange={(e) => { setEmailTouched(true); setEmailDraft(prev => ({ ...prev, username: e.target.value })) }}
                  className="font-mono text-xs"
                />
              </div>
              <div className="space-y-1.5">
                <label className="text-xs font-bold text-muted-foreground uppercase">App-Passwort</label>
                <div className="relative">
                  <Lock className="absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
                  <Input
                    type="password"
                    placeholder={emailInfo?.password_set ? "••••••••  (neu eingeben zum Ändern)" : "App-Passwort eingeben"}
                    value={emailDraft.password}
                    onChange={(e) => { setEmailTouched(true); setEmailDraft(prev => ({ ...prev, password: e.target.value })) }}
                    className="pl-9 font-mono text-xs"
                    autoComplete="new-password"
                  />
                </div>
                <p className={`text-[10px] ${emailInfo?.password_set ? "text-muted-foreground" : "text-destructive"}`}>
                  {emailInfo?.password_set
                    ? emailInfo.password_source === "meta"
                      ? "Gesetzt und AES-verschlüsselt in der Datenbank."
                      : "Gesetzt via EMAIL_PASSWORD — wird beim nächsten Start verschlüsselt übernommen."
                    : "Fehlt — hier eingeben oder EMAIL_PASSWORD setzen."}
                </p>
              </div>
              <div className="space-y-1.5">
                <label className="text-xs font-bold text-muted-foreground uppercase">Lookback (Std.)</label>
                <Input
                  type="number"
                  min={1}
                  max={720}
                  value={emailDraft.lookback_hours}
                  onChange={(e) => { setEmailTouched(true); setEmailDraft(prev => ({ ...prev, lookback_hours: Number(e.target.value) })) }}
                  className="font-mono text-xs"
                />
                <p className="text-[10px] text-muted-foreground">Wie weit zurück geprüft wird (Standard: 72).</p>
              </div>
              <div className="space-y-1.5 sm:col-span-2">
                <label className="text-xs font-bold text-muted-foreground uppercase">From-Filter (optional)</label>
                <Input
                  placeholder="@immobilienscout24.de, no-reply@is24.de"
                  value={emailDraft.senders}
                  onChange={(e) => { setEmailTouched(true); setEmailDraft(prev => ({ ...prev, senders: e.target.value })) }}
                  className="font-mono text-xs"
                />
                <p className="text-[10px] text-muted-foreground">
                  Komma-getrennt, Substring-Match auf <code className="font-mono">From:</code>. Leer = IS24-Standardliste.
                </p>
              </div>
            </div>

            <div className="flex items-center justify-between gap-2 pt-2 border-t border-border/40">
              <span className="text-[10px] text-muted-foreground">
                {emailInfo?.restart_required
                  ? "Änderungen sind gespeichert. Restart erforderlich, damit der Monitor sie übernimmt."
                  : emailInfo?.meta_override
                  ? "Konfiguration aus der Datenbank — überschreibt .env-Werte."
                  : "Konfiguration aus .env / config.yaml."}
              </span>
              <Button
                onClick={() => saveEmail(emailInfo?.enabled ?? false)}
                disabled={savingEmail}
                size="sm"
                className="h-8 px-4 font-semibold gap-1.5"
              >
                <Save className="h-3.5 w-3.5" />
                {savingEmail ? "Speichert…" : "Speichern"}
              </Button>
            </div>
          </CardContent>
        </Card>
        )}

        {view === "profiles" && (
        <Card className="shadow-sm border border-border/60">
          <CardHeader className="flex flex-col gap-3 pb-4 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <CardTitle className="text-md font-bold tracking-tight">Suchprofile</CardTitle>
              <CardDescription className="text-xs">Aktive Suchabfragen auf Immobilienscout24</CardDescription>
            </div>
            
            {/* Add Profile Dialog */}
            <Dialog open={isAddingProfile} onOpenChange={setIsAddingProfile}>
              <DialogTrigger asChild>
                <Button size="sm" className="gap-1.5 font-semibold">
                  <Plus className="h-4 w-4" /> Profil anlegen
                </Button>
              </DialogTrigger>
              <DialogContent className="sm:max-w-[480px]">
                <form onSubmit={handleAddProfile}>
                  <DialogHeader>
                    <DialogTitle>Neues Suchprofil anlegen</DialogTitle>
                    <DialogDescription>
                      Füge eine Immobilienscout24 Such-URL hinzu. Der Bot durchsucht diese regelmäßig.
                    </DialogDescription>
                  </DialogHeader>
                  <div className="space-y-4 py-4">
                    {/* URL */}
                    <div className="space-y-1.5">
                      <label htmlFor="url" className="text-xs font-bold text-muted-foreground uppercase">IS24-Such-URL</label>
                      <Input
                        id="url"
                        placeholder="https://www.immobilienscout24.de/Suche/de/..."
                        value={newProfile.url}
                        onChange={(e) => setNewProfile(prev => ({ ...prev, url: e.target.value }))}
                        required
                        className="font-mono text-xs"
                      />
                    </div>
                    {/* Campaign / Category Select */}
                    <div className="space-y-1.5">
                      <label htmlFor="campaign" className="text-xs font-bold text-muted-foreground uppercase">Kampagne (Bewerbungsprofil)</label>
                      <Select
                        value={selectedProfileCategory}
                        onValueChange={(val) => setNewProfile(prev => ({ ...prev, category: val }))}
                      >
                        <SelectTrigger id="campaign">
                          <SelectValue placeholder="Wähle eine Kampagne" />
                        </SelectTrigger>
                        <SelectContent>
                          {currentOverview.campaigns.map((c) => (
                            <SelectItem key={c} value={c}>
                              {c} {c === currentOverview.default_campaign ? " (Standard)" : ""}
                            </SelectItem>
                          ))}
                        </SelectContent>
                      </Select>
                    </div>
                    {/* Friendly Name */}
                    <div className="space-y-1.5">
                      <label htmlFor="name" className="text-xs font-bold text-muted-foreground uppercase">Name (optional)</label>
                      <Input
                        id="name"
                        placeholder="z.B. Berlin 2-Zimmer"
                        value={newProfile.name}
                        onChange={(e) => setNewProfile(prev => ({ ...prev, name: e.target.value }))}
                      />
                      <p className="text-[10px] text-muted-foreground">Wenn leer gelassen, wird der Name automatisch aus der URL ermittelt.</p>
                    </div>
                  </div>
                  <DialogFooter>
                    <Button
                      type="button"
                      variant="outline"
                      onClick={() => setIsAddingProfile(false)}
                      disabled={submittingProfile}
                    >
                      Abbrechen
                    </Button>
                    <Button type="submit" disabled={submittingProfile} className="font-semibold">
                      {submittingProfile ? "Wird angelegt..." : "Profil speichern"}
                    </Button>
                  </DialogFooter>
                </form>
              </DialogContent>
            </Dialog>
          </CardHeader>
          <CardContent>
            <div className="rounded-md border overflow-hidden">
              <Table>
                <TableHeader className="bg-muted/30">
                  <TableRow>
                    <TableHead className="w-[80px]">ID</TableHead>
                    <TableHead>Name</TableHead>
                    <TableHead className="w-[140px]">Kampagne</TableHead>
                    <TableHead className="w-[100px]">URL</TableHead>
                    <TableHead className="w-[110px] text-center">Status</TableHead>
                    <TableHead className="w-[160px] text-right">Aktionen</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {profiles.length > 0 ? (
                    profiles.map((p) => (
                      <TableRow key={p.id} className="hover:bg-muted/10 transition-colors">
                        <TableCell className="font-mono text-xs text-muted-foreground">#{p.id}</TableCell>
                        <TableCell className="font-semibold">{esc(p.name)}</TableCell>
                        <TableCell>
                          <Badge variant="outline" className="font-medium bg-muted/20">
                            {esc(p.category || currentOverview.default_campaign)}
                          </Badge>
                        </TableCell>
                        <TableCell>
                          {p.search_url ? (
                            <a
                              href={p.search_url}
                              target="_blank"
                              rel="noreferrer"
                              className="inline-flex items-center gap-1 text-xs text-primary hover:underline font-semibold"
                            >
                              Link <ExternalLink className="h-3 w-3" />
                            </a>
                          ) : (
                            <span className="text-xs text-muted-foreground">{esc(p.city)}</span>
                          )}
                        </TableCell>
                        <TableCell className="text-center">
                          <Badge
                            className={`py-0.5 px-2 rounded-full text-[10px] font-semibold tracking-wide ${
                              p.active
                                ? STATUS_TONE.active
                                : "bg-muted text-muted-foreground border border-muted-foreground/10"
                            }`}
                          >
                            {p.active ? "aktiv" : "pausiert"}
                          </Badge>
                        </TableCell>
                        <TableCell className="text-right space-x-2">
                          <Button
                            variant="outline"
                            size="sm"
                            onClick={() => toggleProfile(p.id, !p.active)}
                            className="h-8 px-2.5 font-medium text-xs gap-1.5"
                          >
                            {p.active ? (
                              <><Pause className="h-3.5 w-3.5" /> Pause</>
                            ) : (
                              <><Play className="h-3.5 w-3.5" /> Aktiv</>
                            )}
                          </Button>
                          <Button
                            variant="ghost"
                            size="icon"
                            onClick={() => deleteProfile(p.id)}
                            className="h-8 w-8 text-muted-foreground hover:bg-muted hover:text-foreground"
                          >
                            <Trash2 className="h-4 w-4" />
                          </Button>
                        </TableCell>
                      </TableRow>
                    ))
                  ) : (
                    <TableRow>
                      <TableCell colSpan={6} className="h-24 text-center text-muted-foreground text-sm">
                        Keine Profile vorhanden. Lege oben dein erstes Suchprofil an.
                      </TableCell>
                    </TableRow>
                  )}
                </TableBody>
              </Table>
            </div>
          </CardContent>
        </Card>
        )}

        {view === "templates" && (
        <Card className="shadow-sm border border-border/60">
          <CardHeader className="pb-4">
            <CardTitle className="text-md font-bold tracking-tight flex items-center gap-2">
              <Wand2 className="h-4 w-4" /> Nachrichten-Vorlagen
            </CardTitle>
            <CardDescription className="text-xs">
              AI-Prompt + Nachrichten-Template pro Kampagne. Die AI füllt nur den Platzhalter{" "}
              <code className="rounded bg-muted px-1 py-0.5 font-mono text-[10px]">{"{{.PersonalizedDetails}}"}</code>{" "}
              im Template. Änderungen greifen ab dem nächsten Suchzyklus.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-6">
            {campaigns.length === 0 && (
              <p className="text-sm text-muted-foreground">Keine Kampagnen konfiguriert.</p>
            )}
            {campaigns.map((c) => {
              const d = drafts[c.name] || { ai_prompt: c.ai_prompt, template: c.template }
              const dirty = d.ai_prompt !== c.ai_prompt || d.template !== c.template
              const overridden = c.ai_prompt_overridden || c.template_overridden
              const taClass =
                "w-full rounded-md border border-input bg-transparent px-3 py-2 text-xs font-mono shadow-sm transition-colors placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring resize-y"
              return (
                <div key={c.name} className="rounded-lg border border-border/60 p-4 space-y-4 bg-muted/5">
                  <div className="flex flex-col gap-3 lg:flex-row lg:items-center lg:justify-between">
                    <div className="flex items-center gap-2">
                      <span className="font-semibold text-sm">{c.name}</span>
                      {c.name === currentOverview.default_campaign && (
                        <Badge variant="outline" className="text-[10px] bg-muted/20">Standard</Badge>
                      )}
                      <Badge
                        variant="outline"
                        className={`text-[10px] ${overridden ? STATUS_TONE.medium : "bg-muted/20 text-muted-foreground"}`}
                      >
                        {overridden ? "angepasst" : "Standard (config.yaml)"}
                      </Badge>
                    </div>
                    <div className="flex flex-wrap items-center gap-2">
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => resetCampaign(c.name)}
                        disabled={savingCampaign === c.name || !overridden}
                        className="h-8 gap-1.5 text-xs text-muted-foreground"
                      >
                        <RotateCcw className="h-3.5 w-3.5" /> Zurücksetzen
                      </Button>
                      <Button
                        size="sm"
                        onClick={() => saveCampaign(c.name)}
                        disabled={savingCampaign === c.name || !dirty}
                        className="h-8 gap-1.5 text-xs font-semibold"
                      >
                        <Save className="h-3.5 w-3.5" />
                        {savingCampaign === c.name ? "Speichert..." : "Speichern"}
                      </Button>
                    </div>
                  </div>

                  <div className="space-y-1.5">
                    <label className="flex items-center gap-1.5 text-xs font-bold text-muted-foreground uppercase">
                      <Sparkles className="h-3.5 w-3.5" /> AI-Prompt (System)
                    </label>
                    <textarea
                      className={taClass}
                      rows={5}
                      value={d.ai_prompt}
                      onChange={(e) =>
                        setDrafts(prev => ({ ...prev, [c.name]: { ...d, ai_prompt: e.target.value } }))
                      }
                    />
                  </div>

                  <div className="space-y-1.5">
                    <label className="flex items-center gap-1.5 text-xs font-bold text-muted-foreground uppercase">
                      <FileText className="h-3.5 w-3.5" /> Nachrichten-Template
                    </label>
                    <textarea
                      className={taClass}
                      rows={9}
                      value={d.template}
                      onChange={(e) =>
                        setDrafts(prev => ({ ...prev, [c.name]: { ...d, template: e.target.value } }))
                      }
                    />
                    <p className="text-[10px] text-muted-foreground">
                      Platzhalter: <code className="font-mono">{"{{.PersonalizedDetails}}"}</code>,{" "}
                      <code className="font-mono">{"{{.District}}"}</code>, <code className="font-mono">{"{{.City}}"}</code>,{" "}
                      <code className="font-mono">{"{{.Title}}"}</code>, <code className="font-mono">{"{{.Price}}"}</code>,{" "}
                      <code className="font-mono">{"{{.Rooms}}"}</code>, <code className="font-mono">{"{{.Area}}"}</code>
                    </p>
                  </div>
                </div>
              )
            })}
          </CardContent>
        </Card>
        )}

        {view === "listings" && (
        <Card className="shadow-sm border border-border/60">
          <CardHeader className="flex flex-col gap-3 pb-4 sm:flex-row sm:items-center sm:justify-between">
            <div>
              <CardTitle className="text-md font-bold tracking-tight">Gefundene Wohnungen</CardTitle>
              <CardDescription className="text-xs">
                {filteredListings.length === listings.length
                  ? `Übersicht der ${listings.length} neuesten Immobilienfunde`
                  : `${filteredListings.length} von ${listings.length} Funden`}
              </CardDescription>
            </div>

            {/* Search profile filter + free-text search */}
            <div className="flex w-full flex-col gap-2 sm:w-auto sm:flex-row sm:items-center">
              <Select value={profileFilter} onValueChange={setProfileFilter}>
                <SelectTrigger className="h-9 w-full sm:w-[200px]">
                  <SelectValue placeholder="Alle Suchprofile" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">Alle Suchprofile</SelectItem>
                  {profiles.map((p) => (
                    <SelectItem key={p.id} value={String(p.id)}>
                      {esc(p.name)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <div className="relative w-full sm:max-w-[280px]">
                <Search className="absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
                <Input
                  placeholder="Filtern..."
                  value={searchQuery}
                  onChange={(e) => setSearchQuery(e.target.value)}
                  className="pl-9 h-9"
                />
              </div>
            </div>
          </CardHeader>
          <CardContent>
            {/* Bulk-action toolbar — visible only when at least one row is
                selected. Sticks just above the table so it stays in sight as
                the user scrolls long result lists. */}
            {selectedIds.size > 0 && (
              <div className="sticky top-0 z-10 -mt-2 mb-3 flex flex-wrap items-center gap-2 rounded-md border border-border/60 bg-muted/40 backdrop-blur px-3 py-2 text-xs">
                <span className="font-semibold">
                  {selectedIds.size} ausgewählt
                </span>
                <span className="text-muted-foreground hidden sm:inline">·</span>
                <Button
                  size="sm"
                  variant="outline"
                  className="h-7 gap-1.5 text-xs"
                  disabled={bulkBusy}
                  onClick={() => bulkSetSkipped(true)}
                >
                  <EyeOff className="h-3.5 w-3.5" /> Ignorieren
                </Button>
                <Button
                  size="sm"
                  variant="outline"
                  className="h-7 gap-1.5 text-xs"
                  disabled={bulkBusy}
                  onClick={() => bulkSetSkipped(false)}
                >
                  <RotateCcw className="h-3.5 w-3.5" /> Wieder aufnehmen
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  className="h-7 ml-auto text-xs text-muted-foreground"
                  onClick={clearSelection}
                  disabled={bulkBusy}
                >
                  Auswahl löschen
                </Button>
              </div>
            )}
            <div className="rounded-md border overflow-hidden">
              <Table>
                <TableHeader className="bg-muted/30">
                  <TableRow>
                    <TableHead className="w-[40px]">
                      <input
                        type="checkbox"
                        aria-label="Alle sichtbaren auswählen"
                        checked={allVisibleSelected}
                        ref={(el) => {
                          if (el) el.indeterminate = someVisibleSelected
                        }}
                        onChange={toggleSelectAllVisible}
                        className="h-4 w-4 cursor-pointer accent-foreground"
                      />
                    </TableHead>
                    <TableHead className="w-[140px]">Datum</TableHead>
                    <TableHead>Titel / Adresse</TableHead>
                    <TableHead className="w-[120px]">Preis</TableHead>
                    <TableHead className="w-[120px]">Zimmer / m²</TableHead>
                    <TableHead className="w-[130px]">Kampagne</TableHead>
                    <TableHead className="w-[180px] text-right">Status</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {filteredListings.length > 0 ? (
                    filteredListings.map((l) => {
                      const isSelected = selectedIds.has(l.id)
                      return (
                      <TableRow
                        key={l.id}
                        data-selected={isSelected ? "true" : undefined}
                        className={`cursor-pointer hover:bg-muted/30 transition-colors ${l.skipped ? "opacity-50" : ""} ${isSelected ? "bg-muted/40" : ""}`}
                        onClick={() => setSelectedListing(l)}
                        onContextMenu={(e) => {
                          e.preventDefault()
                          // Right-clicking outside the current selection
                          // resets it to just this row so the menu's "N
                          // selected" semantics are never surprising.
                          if (!isSelected) {
                            setSelectedIds(new Set([l.id]))
                            setLastSelectedId(l.id)
                          }
                          setContextMenu({ x: e.clientX, y: e.clientY, listing: l })
                        }}
                      >
                        <TableCell
                          className="w-[40px]"
                          onClick={(e) => e.stopPropagation()}
                        >
                          <input
                            type="checkbox"
                            aria-label={`Wohnung ${l.id} auswählen`}
                            checked={isSelected}
                            onChange={() => { /* handled via onClick to capture shiftKey */ }}
                            onClick={(e) => {
                              e.stopPropagation()
                              toggleRowSelected(l.id, e.shiftKey)
                            }}
                            className="h-4 w-4 cursor-pointer accent-foreground"
                          />
                        </TableCell>
                        <TableCell className="text-xs text-muted-foreground font-mono">
                          {formatDate(l.created_at)}
                        </TableCell>
                        <TableCell className="py-3">
                          <div className="flex flex-col gap-0.5">
                            <a
                              href={l.url}
                              target="_blank"
                              rel="noreferrer"
                              onClick={(e) => e.stopPropagation()}
                              className="font-semibold text-sm hover:text-primary hover:underline leading-tight inline-flex items-center gap-1 max-w-[450px] truncate"
                            >
                              {esc(l.title)} <ExternalLink className="h-3 w-3 shrink-0 text-muted-foreground/60" />
                            </a>
                            <span className="text-xs text-muted-foreground">
                              {esc(l.address || l.city)}
                            </span>
                          </div>
                        </TableCell>
                        <TableCell className="font-semibold text-sm">
                          {l.price ? (
                            <span className="inline-flex items-center gap-0.5 text-foreground font-mono">
                              {l.price.toLocaleString("de-DE")} €
                            </span>
                          ) : (
                            <span className="text-muted-foreground font-mono">–</span>
                          )}
                        </TableCell>
                        <TableCell className="text-xs text-muted-foreground font-semibold font-mono">
                          {l.rooms || "–"} Zi. / {l.area ? `${l.area} m²` : "–"}
                        </TableCell>
                        <TableCell>
                          <Badge variant="outline" className="font-medium bg-muted/20 text-xs">
                            {esc(l.campaign)}
                          </Badge>
                        </TableCell>
                        <TableCell className="text-right">
                          <div className="inline-flex flex-wrap gap-1.5 justify-end">
                            {l.exclusive_expose && (
                              <Badge
                                variant="outline"
                                className={`h-6 gap-1 text-[10px] font-bold px-2 rounded-full ${STATUS_TONE.subtle}`}
                                title="Nur für Suchen+ Mitglieder kontaktierbar"
                              >
                                <Lock className="h-3 w-3" /> Suchen+
                              </Badge>
                            )}
                            {l.notified && (
                              <Badge
                                variant="outline"
                                className={`h-6 gap-1 text-[10px] font-bold px-2 rounded-full ${STATUS_TONE.medium}`}
                              >
                                <BellRing className="h-3 w-3" /> benachrichtigt
                              </Badge>
                            )}
                            {l.contacted && (
                              <Badge
                                variant="outline"
                                className={`h-6 gap-1 text-[10px] font-bold px-2 rounded-full ${STATUS_TONE.active}`}
                              >
                                <CheckCircle2 className="h-3 w-3" /> kontaktiert
                              </Badge>
                            )}
                            {l.skipped && (
                              <Badge
                                variant="outline"
                                className={`h-6 gap-1 text-[10px] font-bold px-2 rounded-full ${STATUS_TONE.subtle}`}
                                title="Manuell ignoriert — wird nicht automatisch kontaktiert"
                              >
                                <EyeOff className="h-3 w-3" /> ignoriert
                              </Badge>
                            )}
                            {l.rejected && (
                              <Badge
                                variant="outline"
                                className={`h-6 gap-1 text-[10px] font-bold px-2 rounded-full ${STATUS_TONE.quiet}`}
                                title="Approval abgelehnt — Rechtsklick → Verwerfung rückgängig"
                              >
                                <X className="h-3 w-3" /> verworfen
                              </Badge>
                            )}
                            {!l.notified && !l.contacted && !l.exclusive_expose && !l.skipped && !l.rejected && (
                              <span className="text-xs text-muted-foreground italic px-2">Kein Status</span>
                            )}
                          </div>
                        </TableCell>
                      </TableRow>
                      )
                    })
                  ) : (
                    <TableRow>
                      <TableCell colSpan={7} className="h-24 text-center text-muted-foreground text-sm">
                        {searchQuery || profileFilter !== "all" ? "Keine passenden Wohnungen gefunden." : "Noch keine Wohnungen gefunden."}
                      </TableCell>
                    </TableRow>
                  )}
                </TableBody>
              </Table>
            </div>
          </CardContent>
        </Card>
        )}

        {view === "queue" && (
        <div className="space-y-6">
          <Card className="shadow-sm border border-border/60">
            <CardHeader className="pb-4">
              <CardTitle className="text-md font-bold tracking-tight">Aktuell in Telegram</CardTitle>
              <CardDescription className="text-xs">
                {queue.pending
                  ? "Der Bot wartet auf deine Entscheidung. Solange das offen ist, wird keine weitere Wohnung vorgeschlagen."
                  : "Aktuell keine Wohnung im Approval. Der nächste Vorschlag startet beim nächsten Poll-Zyklus."}
              </CardDescription>
            </CardHeader>
            <CardContent>
              {queue.pending ? (
                <div className="space-y-4">
                  <div className="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
                    <div className="space-y-1">
                      <div className="font-semibold leading-snug">{esc(queue.pending.listing.title)}</div>
                      <div className="text-xs text-muted-foreground">
                        {esc(queue.pending.listing.address || queue.pending.listing.city || "–")}
                        {queue.pending.listing.search_profile_name ? ` · ${esc(queue.pending.listing.search_profile_name)}` : ""}
                      </div>
                      <div className="text-xs text-muted-foreground tabular-nums">
                        {typeof queue.pending.listing.price === "number" ? `${queue.pending.listing.price.toLocaleString("de-DE")} €` : "–"}
                        {typeof queue.pending.listing.rooms === "number" ? ` · ${queue.pending.listing.rooms} Zi` : ""}
                        {typeof queue.pending.listing.area === "number" ? ` · ${queue.pending.listing.area} m²` : ""}
                      </div>
                      <div className="text-[10px] uppercase tracking-wide text-muted-foreground">
                        Vorgeschlagen: {formatDate(queue.pending.created_at)}
                      </div>
                    </div>
                    <div className="flex gap-2">
                      <Button
                        size="sm"
                        className="gap-2"
                        onClick={() => decidePending("approve")}
                        disabled={queueBusy !== null}
                      >
                        <Check className="h-4 w-4" />
                        {queueBusy === "approve" ? "Sende…" : "Senden"}
                      </Button>
                      <Button
                        size="sm"
                        variant="outline"
                        className="gap-2"
                        onClick={() => decidePending("reject")}
                        disabled={queueBusy !== null}
                      >
                        <X className="h-4 w-4" />
                        {queueBusy === "reject" ? "Verwerfe…" : "Verwerfen"}
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        asChild
                      >
                        <a href={queue.pending.listing.url} target="_blank" rel="noopener noreferrer" className="gap-1 inline-flex items-center">
                          <ExternalLink className="h-4 w-4" />
                          IS24
                        </a>
                      </Button>
                    </div>
                  </div>
                  <div className="rounded-md border bg-muted/20 p-3">
                    <div className="text-[10px] uppercase tracking-wide text-muted-foreground mb-2">Generierte Nachricht</div>
                    <pre className="whitespace-pre-wrap text-xs font-mono leading-relaxed">{queue.pending.message}</pre>
                  </div>
                </div>
              ) : (
                <div className="text-sm text-muted-foreground py-6 text-center">
                  Keine offene Approval-Karte. ✅ / ❌ via Telegram oder hier.
                </div>
              )}
            </CardContent>
          </Card>

          <Card className="shadow-sm border border-border/60">
            <CardHeader className="pb-4">
              <CardTitle className="text-md font-bold tracking-tight">Als nächstes in der Queue</CardTitle>
              <CardDescription className="text-xs">
                {queue.next.length === 0
                  ? "Keine weiteren Wohnungen warten. Sobald der nächste Poll-Lauf neue findet, landen sie hier."
                  : `${queue.next.length} Wohnung${queue.next.length === 1 ? "" : "en"} in der Reihenfolge, wie der Bot sie vorschlagen wird.`}
              </CardDescription>
            </CardHeader>
            <CardContent>
              <div className="rounded-md border overflow-hidden">
                <Table>
                  <TableHeader className="bg-muted/30">
                    <TableRow>
                      <TableHead className="w-[60px]">#</TableHead>
                      <TableHead>Wohnung</TableHead>
                      <TableHead className="w-[140px]">Profil</TableHead>
                      <TableHead className="w-[120px] text-right">Preis</TableHead>
                      <TableHead className="w-[100px] text-right">Zimmer/m²</TableHead>
                      <TableHead className="w-[140px]">Gefunden</TableHead>
                      <TableHead className="w-[80px]"></TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {queue.next.length > 0 ? (
                      queue.next.map((l, i) => (
                        <TableRow key={l.id} className="hover:bg-muted/10 transition-colors">
                          <TableCell className="font-mono text-xs text-muted-foreground">{i + 1}</TableCell>
                          <TableCell>
                            <div className="font-medium leading-tight">{esc(l.title)}</div>
                            <div className="text-xs text-muted-foreground">{esc(l.address || l.city || "–")}</div>
                          </TableCell>
                          <TableCell className="text-xs text-muted-foreground">{esc(l.search_profile_name || "–")}</TableCell>
                          <TableCell className="text-right tabular-nums">
                            {typeof l.price === "number" ? `${l.price.toLocaleString("de-DE")} €` : "–"}
                          </TableCell>
                          <TableCell className="text-right tabular-nums text-xs text-muted-foreground">
                            {typeof l.rooms === "number" ? `${l.rooms} Zi` : "–"}
                            {typeof l.area === "number" ? ` / ${l.area} m²` : ""}
                          </TableCell>
                          <TableCell className="text-xs text-muted-foreground">{formatDate(l.created_at)}</TableCell>
                          <TableCell className="text-right">
                            <Button variant="ghost" size="sm" asChild>
                              <a href={l.url} target="_blank" rel="noopener noreferrer">
                                <ExternalLink className="h-4 w-4" />
                              </a>
                            </Button>
                          </TableCell>
                        </TableRow>
                      ))
                    ) : (
                      <TableRow>
                        <TableCell colSpan={7} className="h-24 text-center text-muted-foreground text-sm">
                          Keine wartenden Wohnungen.
                        </TableCell>
                      </TableRow>
                    )}
                  </TableBody>
                </Table>
              </div>
            </CardContent>
          </Card>
        </div>
        )}

        {view === "inbox" && (
        <Card className="shadow-sm border border-border/60">
          <CardHeader className="pb-4">
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div className="space-y-1">
                <CardTitle className="text-md font-bold tracking-tight">Posteingang</CardTitle>
                <CardDescription className="text-xs">
                  {inbox.length}{inboxLandlordOnly ? " gefilterte" : ""} IS24-E-Mails — markiert sind echte
                  Anbieter-Antworten, die nicht über den IS24-Chat kamen.
                </CardDescription>
              </div>
              <div className="flex items-center gap-4">
                <label className="flex items-center gap-2 text-xs text-muted-foreground cursor-pointer">
                  <Switch
                    checked={inboxLandlordOnly}
                    onCheckedChange={setInboxLandlordOnly}
                  />
                  Nur Anbieter-Antworten
                </label>
                <Button
                  size="sm"
                  variant="outline"
                  onClick={() => void triggerInboxScan()}
                  disabled={inboxScanning}
                >
                  {inboxScanning ? "Scanne…" : "Jetzt scannen"}
                </Button>
              </div>
            </div>
            {/* Scan progress / result panel. Lives in the header so it stays
                visible even when the table below scrolls. Spinner shows while
                the POST is in flight; result counts and per-mail errors land
                here once the call returns. */}
            {(inboxScanning || inboxScanResult || inboxScanError) && (
              <div className="mt-3 rounded-md border bg-muted/20 px-3 py-2 text-xs">
                {inboxScanning && (
                  <div className="flex items-center gap-2 text-muted-foreground">
                    <span className="inline-block h-2 w-2 animate-pulse rounded-full bg-primary" />
                    Scanne IMAP-Postfach (max. 30 s)…
                  </div>
                )}
                {!inboxScanning && inboxScanResult && (
                  <div className="space-y-1">
                    <div className="flex flex-wrap gap-x-3 gap-y-1 font-mono">
                      <span><strong>{inboxScanResult.inspected}</strong> im Zeitfenster</span>
                      <span className="text-muted-foreground">·</span>
                      <span>{inboxScanResult.fetched} von IS24-Absendern</span>
                      <span className="text-muted-foreground">·</span>
                      <span>{inboxScanResult.classified} neu klassifiziert</span>
                      <span className="text-muted-foreground">·</span>
                      <span className={inboxScanResult.landlord_replies > 0 ? "font-bold text-emerald-600" : ""}>
                        {inboxScanResult.landlord_replies} Vermieter-Antwort{inboxScanResult.landlord_replies === 1 ? "" : "en"}
                      </span>
                      <span className="text-muted-foreground">·</span>
                      <span>{formatScanDuration(inboxScanResult.duration_ms)}</span>
                    </div>
                    {/* Inspected>0 but Fetched=0 = the user has new mail, none of it from the
                        configured senders. This is the dominant "why didn't anything happen"
                        case so we explain it inline instead of leaving the user to guess. */}
                    {inboxScanResult.inspected > 0 && inboxScanResult.fetched === 0 && (
                      <div className="text-amber-700 dark:text-amber-400">
                        Im Zeitfenster lagen {inboxScanResult.inspected} neue Mail{inboxScanResult.inspected === 1 ? "" : "s"},
                        aber keine passte zum aktuellen Absender-Filter
                        {inboxScanResult.senders && inboxScanResult.senders.length > 0 && (
                          <> <span className="font-mono">[{inboxScanResult.senders.join(", ")}]</span></>
                        )}
                        . Filter in den Einstellungen anpassen.
                      </div>
                    )}
                    {inboxScanResult.errors && inboxScanResult.errors.length > 0 && (
                      <div className="space-y-0.5 text-destructive">
                        <div className="font-semibold">
                          {inboxScanResult.errors.length} Mail-Fehler:
                        </div>
                        <ul className="list-inside list-disc pl-1 font-mono">
                          {inboxScanResult.errors.map((e, i) => (
                            <li key={i}>{e}</li>
                          ))}
                        </ul>
                      </div>
                    )}
                  </div>
                )}
                {!inboxScanning && inboxScanError && !inboxScanResult && (
                  <div className="text-destructive">
                    <span className="font-semibold">Scan fehlgeschlagen:</span>{" "}
                    <span className="font-mono">{inboxScanError}</span>
                  </div>
                )}
              </div>
            )}
          </CardHeader>
          <CardContent>
            <div className="rounded-md border overflow-hidden">
              <Table>
                <TableHeader className="bg-muted/30">
                  <TableRow>
                    <TableHead className="w-[140px]">Datum</TableHead>
                    <TableHead className="w-[200px]">Von</TableHead>
                    <TableHead>Betreff / Zusammenfassung</TableHead>
                    <TableHead className="w-[160px] text-right">Status</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {inbox.length > 0 ? (
                    inbox.map((m) => (
                      <TableRow key={m.id} className="hover:bg-muted/10 transition-colors">
                        <TableCell className="text-xs text-muted-foreground font-mono">
                          {formatDate(m.received_at || m.created_at)}
                        </TableCell>
                        <TableCell className="text-xs text-muted-foreground max-w-[200px] truncate">
                          {esc(m.from)}
                        </TableCell>
                        <TableCell className="py-3">
                          <div className="flex flex-col gap-0.5">
                            <span className="font-semibold text-sm leading-tight">{esc(m.subject) || "(kein Betreff)"}</span>
                            {m.summary && (
                              <span className="text-xs text-muted-foreground">{esc(m.summary)}</span>
                            )}
                            {m.is24_id && (
                              <a
                                href={`https://www.immobilienscout24.de/expose/${m.is24_id}`}
                                target="_blank"
                                rel="noreferrer"
                                className="text-xs text-muted-foreground hover:text-primary hover:underline inline-flex items-center gap-1"
                              >
                                Exposé {m.is24_id} <ExternalLink className="h-3 w-3 shrink-0 text-muted-foreground/60" />
                              </a>
                            )}
                          </div>
                        </TableCell>
                        <TableCell className="text-right">
                          {m.is_landlord_reply ? (
                            <Badge
                              variant="outline"
                              className={`h-6 gap-1 text-[10px] font-bold px-2 rounded-full ${STATUS_TONE.active}`}
                            >
                              <Mail className="h-3 w-3" /> Anbieter-Antwort
                            </Badge>
                          ) : (
                            <span className="text-xs text-muted-foreground italic px-2">System / Info</span>
                          )}
                        </TableCell>
                      </TableRow>
                    ))
                  ) : (
                    <TableRow>
                      <TableCell colSpan={4} className="h-24 text-center text-muted-foreground text-sm">
                        Noch keine IS24-E-Mails erkannt.
                      </TableCell>
                    </TableRow>
                  )}
                </TableBody>
              </Table>
            </div>
          </CardContent>
        </Card>
        )}

        {/* Listing detail drawer — opens on row click in the Wohnungen view. */}
        <Sheet
          open={selectedListing !== null}
          onOpenChange={(open) => {
            if (!open) setSelectedListing(null)
          }}
        >
          <SheetContent side="right" className="overflow-y-auto">
            {selectedListing && (
              <>
                <SheetHeader>
                  <SheetTitle className="break-words">{selectedListing.title || "Wohnung"}</SheetTitle>
                  <SheetDescription>
                    {selectedListing.search_profile_name && (
                      <>Profil: <span className="font-medium text-foreground">{selectedListing.search_profile_name}</span>{" · "}</>
                    )}
                    {selectedListing.campaign && <>Kampagne: {selectedListing.campaign}</>}
                  </SheetDescription>
                </SheetHeader>

                <div className="space-y-4 text-xs">
                  {/* Status row */}
                  <div className="flex flex-wrap gap-1.5">
                    {selectedListing.exclusive_expose && (
                      <Badge variant="outline" className={`h-6 gap-1 text-[10px] font-bold px-2 rounded-full ${STATUS_TONE.subtle}`}>
                        <Lock className="h-3 w-3" /> Suchen+ exklusiv
                      </Badge>
                    )}
                    {selectedListing.notified && (
                      <Badge variant="outline" className={`h-6 gap-1 text-[10px] font-bold px-2 rounded-full ${STATUS_TONE.medium}`}>
                        <BellRing className="h-3 w-3" /> benachrichtigt
                      </Badge>
                    )}
                    {selectedListing.contacted && (
                      <Badge variant="outline" className={`h-6 gap-1 text-[10px] font-bold px-2 rounded-full ${STATUS_TONE.active}`}>
                        <CheckCircle2 className="h-3 w-3" /> kontaktiert
                      </Badge>
                    )}
                  </div>

                  {/* Suchen+ explanation banner */}
                  {selectedListing.exclusive_expose && (
                    <div className="rounded-md border border-amber-400/40 bg-amber-50 dark:bg-amber-950/30 p-3 text-xs leading-relaxed">
                      <p className="font-semibold mb-1">Diese Anzeige ist Suchen+ exklusiv.</p>
                      <p className="text-muted-foreground">
                        IS24 versteckt das Kontaktformular hinter einer kostenpflichtigen Mitgliedschaft. Der Bot
                        verschickt für diese Wohnung keine Nachricht. Du kannst sie nur direkt auf IS24 mit aktivem
                        Suchen+ Abo kontaktieren.
                      </p>
                    </div>
                  )}

                  {/* Quick facts */}
                  <div className="grid grid-cols-2 gap-2 rounded-md border p-3 bg-muted/20">
                    <DetailRow label="Preis" value={selectedListing.price ? `${selectedListing.price.toLocaleString("de-DE")} €` : "–"} />
                    <DetailRow label="m²" value={selectedListing.area ? `${selectedListing.area}` : "–"} />
                    <DetailRow label="Zimmer" value={selectedListing.rooms ? `${selectedListing.rooms}` : "–"} />
                    <DetailRow label="Baujahr" value={selectedListing.build_year ? `${selectedListing.build_year}` : "–"} />
                    <DetailRow label="Balkon" value={selectedListing.has_balcony ? "ja" : "–"} />
                    <DetailRow label="EBK" value={selectedListing.has_ebk ? "ja" : "–"} />
                    <DetailRow label="Aufzug" value={selectedListing.has_elevator ? "ja" : "–"} />
                    <DetailRow label="Ab" value={selectedListing.available_from || "–"} />
                  </div>

                  {/* Location */}
                  <div className="rounded-md border p-3 bg-muted/10 space-y-1.5">
                    <DetailRow label="Adresse" value={selectedListing.address || "–"} />
                    <DetailRow label="PLZ / Stadt" value={[selectedListing.postal_code, selectedListing.city].filter(Boolean).join(" ") || "–"} />
                    <DetailRow label="Stadtteil" value={selectedListing.district || "–"} />
                    <DetailRow label="Ansprechpartner" value={selectedListing.contact_person || selectedListing.landlord_name || "–"} />
                  </div>

                  {/* Description */}
                  {selectedListing.description && (
                    <div className="rounded-md border p-3 bg-muted/10 space-y-1.5">
                      <div className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">Beschreibung</div>
                      <p className="whitespace-pre-wrap text-xs leading-relaxed">{selectedListing.description}</p>
                    </div>
                  )}

                  {/* External link */}
                  <a
                    href={selectedListing.url}
                    target="_blank"
                    rel="noreferrer"
                    className="inline-flex items-center gap-1 text-xs font-semibold text-primary hover:underline"
                  >
                    Auf IS24 ansehen <ExternalLink className="h-3 w-3" />
                  </a>

                  {/* Messages timeline */}
                  <div className="space-y-2">
                    <div className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">Nachrichten</div>
                    {loadingMessages ? (
                      <div className="text-xs text-muted-foreground italic">lädt…</div>
                    ) : listingMessages.length === 0 ? (
                      <div className="text-xs text-muted-foreground italic">Noch keine Nachricht generiert.</div>
                    ) : (
                      <ul className="space-y-2">
                        {listingMessages.map((m) => (
                          <li key={m.id} className="rounded-md border p-3 bg-muted/10 space-y-2">
                            <div className="flex items-center justify-between gap-2">
                              <MessageStatusBadge status={m.status} />
                              <span className="text-[10px] text-muted-foreground font-mono">
                                {formatDate(m.sent_at || m.created_at)}
                              </span>
                            </div>
                            <pre className="whitespace-pre-wrap break-words text-[11px] leading-relaxed font-sans text-foreground">{m.message}</pre>
                            {m.error_msg && (
                              <div className="text-[10px] text-red-600 dark:text-red-400 italic">{m.error_msg}</div>
                            )}
                          </li>
                        ))}
                      </ul>
                    )}
                  </div>
                </div>
              </>
            )}
          </SheetContent>
        </Sheet>
      </main>
    </div>
  )
}

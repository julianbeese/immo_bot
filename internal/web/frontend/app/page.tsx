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
  Cookie as CookieIcon
} from "lucide-react"

type View = "overview" | "settings" | "profiles" | "templates" | "listings" | "inbox"

const VIEWS: { key: View; label: string; icon: React.ComponentType<{ className?: string }> }[] = [
  { key: "overview",  label: "Übersicht",         icon: LayoutDashboard },
  { key: "settings",  label: "Einstellungen",     icon: Settings },
  { key: "profiles",  label: "Suchprofile",       icon: Building },
  { key: "templates", label: "Nachrichten",       icon: Wand2 },
  { key: "listings",  label: "Wohnungen",         icon: Home },
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

// Helper to escape HTML if needed, though React handles escaping automatically
const esc = (s?: string) => s || ""

interface Stats {
  total: number
  notified: number
  contacted: number
}

interface Overview {
  contact_mode: "off" | "notify" | "test" | "on"
  contact_label: string
  quiet_hours: boolean
  quiet_hours_start: string
  quiet_hours_end: string
  last_poll: string
  default_campaign: string
  campaigns: string[]
  stats: Stats
}

interface CookieInfo {
  present: boolean
  length: number
  source: "env" | "meta" | "none"
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

interface Listing {
  id: number
  title: string
  url: string
  address?: string
  city?: string
  price?: number
  rooms?: number
  area?: number
  campaign?: string
  search_profile_id?: number
  notified: boolean
  contacted: boolean
  skipped: boolean
  created_at: string
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
}

function sectionSubtitle(view: View, o: Overview): string {
  switch (view) {
    case "overview":  return `${o.stats.total} Wohnungen gefunden, ${o.stats.notified} benachrichtigt, ${o.stats.contacted} kontaktiert.`
    case "settings":  return "Kontakt-Modus, Ruhezeiten und IS24-Cookie."
    case "profiles":  return "IS24-Suchen, die der Bot zyklisch abfragt."
    case "templates": return "AI-Prompt und Nachrichten-Template pro Kampagne."
    case "listings":  return "Gefundene Wohnungen aller Profile."
    case "inbox":     return "IS24-E-Mails: erkannte Anbieter-Antworten außerhalb des Chats."
  }
}

export default function DashboardPage() {
  const { resolvedTheme, setTheme } = useTheme()
  
  // Dashboard state
  const [overview, setOverview] = React.useState<Overview | null>(null)
  const [profiles, setProfiles] = React.useState<SearchProfile[]>([])
  const [listings, setListings] = React.useState<Listing[]>([])
  const [inbox, setInbox] = React.useState<InboxMessage[]>([])
  const [campaigns, setCampaigns] = React.useState<CampaignCfg[]>([])
  // Editable buffers keyed by campaign name; populated from /api/campaigns.
  const [drafts, setDrafts] = React.useState<Record<string, { ai_prompt: string; template: string }>>({})
  const [savingCampaign, setSavingCampaign] = React.useState<string | null>(null)

  // Cookie status (presence only — the cookie itself is never returned by the API).
  const [cookieInfo, setCookieInfo] = React.useState<CookieInfo | null>(null)
  const [cookieDraft, setCookieDraft] = React.useState("")
  const [savingCookie, setSavingCookie] = React.useState(false)

  // Active section selected via the sidebar.
  const [view, setView] = React.useState<View>("overview")
  
  // UI states
  const [loading, setLoading] = React.useState(true)
  const [refreshing, setRefreshing] = React.useState(false)
  const [error, setError] = React.useState<string | null>(null)
  const [searchQuery, setSearchQuery] = React.useState("")
  // Listing filter by search profile id; "all" = no filter.
  const [profileFilter, setProfileFilter] = React.useState<string>("all")
  
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

      const inData = await api("/api/inbox?limit=100")
      setInbox(inData || [])

      const cData: CampaignCfg[] = (await api("/api/campaigns")) || []
      setCampaigns(cData)

      const ckData: CookieInfo = await api("/api/cookie")
      setCookieInfo(ckData)
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

  // Set Settings (Auto Contact Mode / Quiet Hours)
  const setSetting = async (body: {
    contact_mode?: string
    quiet_hours?: boolean
    quiet_hours_start?: string
    quiet_hours_end?: string
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

  // Toggle a listing's manual skip flag (exclude from / return to auto-contact).
  const toggleSkip = async (id: number, skipped: boolean) => {
    try {
      await api(`/api/listings/${id}/skip`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ skipped }),
      })
      setListings(prev => prev.map(l => (l.id === id ? { ...l, skipped } : l)))
      toast.success(skipped ? "Als ignoriert markiert" : "Wieder aktiviert")
    } catch (e: unknown) {
      toast.error("Fehler beim Umschalten", {
        description: errorMessage(e, "Status konnte nicht geändert werden."),
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
                : currentOverview.contact_mode === "test"
                ? STATUS_TONE.medium
                : STATUS_TONE.quiet
            }`}
            title={currentOverview.contact_label}
          >
            <span
              className={`h-1.5 w-1.5 rounded-full ${
                currentOverview.contact_mode === "on"
                  ? "bg-background animate-ping dark:bg-background"
                  : currentOverview.contact_mode === "test"
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
                <div className="flex w-full flex-col gap-3 rounded-lg border p-3 bg-muted/10">
                  <div className="grid grid-cols-2 gap-1 rounded-md border p-1 bg-muted/40 text-xs sm:grid-cols-4">
                    <Button
                      variant={currentOverview.contact_mode === "off" ? "default" : "ghost"}
                      size="sm"
                      onClick={() => setSetting({ contact_mode: "off" })}
                      className="h-7 gap-1 px-3 text-xs font-medium rounded"
                    >
                      <Pause className="h-3 w-3" /> Pausiert
                    </Button>
                    <Button
                      variant={currentOverview.contact_mode === "notify" ? "default" : "ghost"}
                      size="sm"
                      onClick={() => setSetting({ contact_mode: "notify" })}
                      className="h-7 gap-1 px-3 text-xs font-medium rounded"
                    >
                      <BellRing className="h-3 w-3" /> Melden
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
                  <p className="text-xs text-muted-foreground leading-relaxed">
                    {currentOverview.contact_mode === "off"
                      ? "Pausiert: keine Meldungen, kein Anschreiben."
                      : currentOverview.contact_mode === "notify"
                      ? "Melden: neue Wohnungen werden gefunden und gemeldet, aber nicht angeschrieben."
                      : currentOverview.contact_mode === "test"
                      ? "Test: Wohnungen gemeldet, Nachricht-Vorschau (nichts gesendet)."
                      : "Live: neue Wohnungen werden automatisch angeschrieben."}
                  </p>
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
                        : currentOverview.contact_mode === "test"
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
            <div className="rounded-md border overflow-hidden">
              <Table>
                <TableHeader className="bg-muted/30">
                  <TableRow>
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
                    filteredListings.map((l) => (
                      <TableRow key={l.id} className="hover:bg-muted/10 transition-colors">
                        <TableCell className="text-xs text-muted-foreground font-mono">
                          {formatDate(l.created_at)}
                        </TableCell>
                        <TableCell className="py-3">
                          <div className="flex flex-col gap-0.5">
                            <a
                              href={l.url}
                              target="_blank"
                              rel="noreferrer"
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
                          <div className="inline-flex flex-wrap items-center gap-1.5 justify-end">
                            {l.skipped && (
                              <Badge
                                variant="outline"
                                className={`h-6 gap-1 text-[10px] font-bold px-2 rounded-full ${STATUS_TONE.subtle}`}
                              >
                                <EyeOff className="h-3 w-3" /> ignoriert
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
                            {!l.notified && !l.contacted && !l.skipped && (
                              <span className="text-xs text-muted-foreground italic px-2">Kein Status</span>
                            )}
                            <Button
                              variant="ghost"
                              size="sm"
                              onClick={() => toggleSkip(l.id, !l.skipped)}
                              title={l.skipped ? "Wieder für Auto-Kontakt freigeben" : "Vom Auto-Kontakt ausschließen"}
                              className="h-6 w-6 p-0 text-muted-foreground hover:text-foreground"
                            >
                              {l.skipped ? <RotateCcw className="h-3.5 w-3.5" /> : <EyeOff className="h-3.5 w-3.5" />}
                            </Button>
                          </div>
                        </TableCell>
                      </TableRow>
                    ))
                  ) : (
                    <TableRow>
                      <TableCell colSpan={6} className="h-24 text-center text-muted-foreground text-sm">
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

        {view === "inbox" && (
        <Card className="shadow-sm border border-border/60">
          <CardHeader className="pb-4">
            <CardTitle className="text-md font-bold tracking-tight">Posteingang</CardTitle>
            <CardDescription className="text-xs">
              {inbox.length} IS24-E-Mails — markiert sind echte Anbieter-Antworten, die nicht über den IS24-Chat kamen.
            </CardDescription>
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
      </main>
    </div>
  )
}

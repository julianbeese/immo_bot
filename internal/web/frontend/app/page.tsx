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
  Power,
  Home,
  CheckCircle2,
  BellRing,
  Eye,
  Settings,
  Search,
  Check,
  AlertTriangle,
  Building,
  DollarSign,
  Maximize
} from "lucide-react"

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
  contact_mode: "off" | "test" | "on"
  contact_label: string
  quiet_hours: boolean
  last_poll: string
  default_campaign: string
  campaigns: string[]
  stats: Stats
}

interface SearchProfile {
  id: number
  name: string
  category: string
  search_url?: string
  city?: string
  active: boolean
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
  notified: boolean
  contacted: boolean
  created_at: string
}

export default function DashboardPage() {
  const { resolvedTheme, setTheme } = useTheme()
  
  // Dashboard state
  const [overview, setOverview] = React.useState<Overview | null>(null)
  const [profiles, setProfiles] = React.useState<SearchProfile[]>([])
  const [listings, setListings] = React.useState<Listing[]>([])
  
  // UI states
  const [loading, setLoading] = React.useState(true)
  const [refreshing, setRefreshing] = React.useState(false)
  const [error, setError] = React.useState<string | null>(null)
  const [searchQuery, setSearchQuery] = React.useState("")
  
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
      
      setError(null)
    } catch (e: any) {
      console.error(e)
      setError(e.message || "Fehler beim Laden der Daten")
      toast.error("Verbindungsfehler", {
        description: e.message || "Daten konnten nicht aktualisiert werden.",
      })
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [api])

  // Initial and Interval polling
  React.useEffect(() => {
    loadData()
    const interval = setInterval(() => loadData(true), 10000)
    return () => clearInterval(interval)
  }, [loadData])

  // Set Settings (Auto Contact Mode / Quiet Hours)
  const setSetting = async (body: { contact_mode?: string; quiet_hours?: boolean }) => {
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
    } catch (e: any) {
      toast.error("Fehler", {
        description: e.message || "Einstellungen konnten nicht gespeichert werden.",
      })
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
    } catch (e: any) {
      toast.error("Fehler beim Umschalten", {
        description: e.message,
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
    } catch (e: any) {
      toast.error("Löschen fehlgeschlagen", {
        description: e.message,
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
    try {
      await api("/api/profiles", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          url: newProfile.url.trim(),
          category: newProfile.category || undefined,
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
    } catch (e: any) {
      toast.error("Erstellen fehlgeschlagen", {
        description: e.message,
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

  // Filter listings based on search query
  const filteredListings = React.useMemo(() => {
    if (!searchQuery.trim()) return listings
    const q = searchQuery.toLowerCase()
    return listings.filter(
      l =>
        l.title.toLowerCase().includes(q) ||
        (l.address && l.address.toLowerCase().includes(q)) ||
        (l.city && l.city.toLowerCase().includes(q)) ||
        (l.campaign && l.campaign.toLowerCase().includes(q))
    )
  }, [listings, searchQuery])

  // Setup default category in form when overview loads
  React.useEffect(() => {
    if (overview && !newProfile.category) {
      setNewProfile(prev => ({
        ...prev,
        category: overview.default_campaign || overview.campaigns[0] || "",
      }))
    }
  }, [overview, newProfile.category])

  if (loading && !overview) {
    return (
      <div className="flex h-screen w-screen flex-col items-center justify-center gap-4 bg-background text-foreground transition-colors duration-300">
        <Home className="h-12 w-12 text-primary animate-pulse" />
        <p className="text-sm font-medium animate-pulse">ImmoBot Dashboard lädt...</p>
      </div>
    )
  }

  const currentOverview = overview!

  return (
    <div className="min-h-screen bg-background text-foreground transition-colors duration-300 antialiased font-sans">
      <Toaster position="bottom-right" />
      
      {/* Top Header */}
      <header className="sticky top-0 z-40 w-full border-b bg-background/95 backdrop-blur-md">
        <div className="container mx-auto flex h-16 max-w-[1440px] items-center justify-between px-4 sm:px-6 md:px-8">
          <div className="flex items-center gap-2.5">
            <div>
              <h1 className="text-lg font-bold tracking-tight">ImmoBot</h1>
              <p className="hidden text-[10px] text-muted-foreground sm:block">Real-time Real Estate Assistant</p>
            </div>
          </div>

          <div className="flex items-center gap-3">
            <Badge
              variant="outline"
              className={`hidden gap-1.5 py-1 px-2.5 text-xs font-semibold sm:flex transition-all duration-300 ${
                currentOverview.contact_mode === "on"
                  ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-500"
                  : currentOverview.contact_mode === "test"
                  ? "border-amber-500/30 bg-amber-500/10 text-amber-500"
                  : "border-muted/50 bg-muted/20 text-muted-foreground"
              }`}
            >
              <span className={`h-1.5 w-1.5 rounded-full ${
                currentOverview.contact_mode === "on"
                  ? "bg-emerald-500 animate-ping"
                  : currentOverview.contact_mode === "test"
                  ? "bg-amber-500 animate-pulse"
                  : "bg-muted-foreground"
              }`} />
              {currentOverview.contact_label} {currentOverview.quiet_hours ? "· 🌙 Ruhezeit" : "· ☀️ 24/7"}
            </Badge>

            <span className="hidden text-xs text-muted-foreground md:inline">
              {currentOverview.last_poll ? (
                <>letzter Poll: <span className="font-semibold text-foreground">{formatDate(currentOverview.last_poll)}</span></>
              ) : (
                "noch kein Poll"
              )}
            </span>

            <Button
              variant="ghost"
              size="icon"
              className="h-9 w-9"
              onClick={() => setTheme(resolvedTheme === "dark" ? "light" : "dark")}
              title="Design umschalten (Taste 'D')"
            >
              {resolvedTheme === "dark" ? (
                <Sun className="h-[1.2rem] w-[1.2rem] text-amber-500 transition-all" />
              ) : (
                <Moon className="h-[1.2rem] w-[1.2rem] text-primary transition-all" />
              )}
              <span className="sr-only">Design umschalten</span>
            </Button>

            <Button
              variant="outline"
              size="icon"
              className={`h-9 w-9 transition-transform duration-300 ${refreshing ? "rotate-180" : ""}`}
              onClick={() => loadData(true)}
              disabled={refreshing}
            >
              <RefreshCw className={`h-4 w-4 ${refreshing ? "animate-spin" : ""}`} />
            </Button>
          </div>
        </div>
      </header>

      {/* Main Body */}
      <main className="container mx-auto max-w-[1440px] p-4 sm:p-6 md:p-8 space-y-6">
        
        {error && (
          <div className="flex items-center gap-3 rounded-lg border border-destructive/20 bg-destructive/10 p-4 text-sm text-destructive">
            <ShieldAlert className="h-5 w-5 shrink-0" />
            <div>
              <h5 className="font-semibold">Verbindungsfehler zum Backend</h5>
              <p className="text-xs text-destructive/80 mt-0.5">{error}</p>
            </div>
            <Button
              variant="outline"
              size="sm"
              className="ml-auto border-destructive/20 hover:bg-destructive/20 text-destructive bg-transparent"
              onClick={() => loadData()}
            >
              Erneut versuchen
            </Button>
          </div>
        )}

        {/* Global Settings & Stats Grid */}
        <div className="grid gap-6 md:grid-cols-12">
          {/* Settings Box */}
          <Card className="md:col-span-5 shadow-sm border border-border/60 hover:border-border transition-all duration-300">
            <CardHeader className="pb-4">
              <CardTitle className="flex items-center gap-2 text-sm font-semibold uppercase tracking-wider text-muted-foreground">
                <Settings className="h-4 w-4" /> Einstellungen
              </CardTitle>
            </CardHeader>
            <CardContent className="space-y-6">
              {/* Contact Mode Select */}
              <div className="space-y-2">
                <label className="text-xs font-semibold text-muted-foreground">Kontakt-Modus</label>
                <div className="flex w-full items-center justify-between rounded-lg border p-2 bg-muted/10">
                  <span className="text-sm font-medium">Bewerbungen:</span>
                  <div className="inline-flex rounded-md border p-1 bg-muted/40 text-xs">
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
                      variant={currentOverview.contact_mode === "on" ? "default" : "ghost"}
                      size="sm"
                      onClick={() => setSetting({ contact_mode: "on" })}
                      className={`h-7 gap-1 px-3 text-xs font-medium rounded transition-colors ${
                        currentOverview.contact_mode === "on"
                          ? "bg-emerald-600 hover:bg-emerald-600 text-white font-semibold shadow-sm"
                          : ""
                      }`}
                    >
                      <Play className="h-3 w-3" /> Live
                    </Button>
                  </div>
                </div>
              </div>

              {/* Quiet Hours Switch */}
              <div className="flex items-center justify-between rounded-lg border p-3 bg-muted/10">
                <div className="flex flex-col gap-0.5">
                  <span className="text-sm font-semibold">Ruhezeiten aktivieren</span>
                  <span className="text-xs text-muted-foreground">Keine automatischen Bewerbungen nachts</span>
                </div>
                <div className="flex items-center gap-3">
                  <div className="flex h-8 w-8 items-center justify-center rounded-full bg-muted/30">
                    {currentOverview.quiet_hours ? (
                      <Moon className="h-4 w-4 text-indigo-500 fill-indigo-500/20" />
                    ) : (
                      <Sun className="h-4 w-4 text-amber-500" />
                    )}
                  </div>
                  <Switch
                    checked={currentOverview.quiet_hours}
                    onCheckedChange={(checked) => setSetting({ quiet_hours: checked })}
                  />
                </div>
              </div>
            </CardContent>
          </Card>

          {/* Stats Cards Box */}
          <div className="md:col-span-7 grid grid-cols-3 gap-4">
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
                <CardDescription className="text-xs font-semibold uppercase tracking-wider text-amber-500">Notifiziert</CardDescription>
              </CardHeader>
              <CardContent className="pb-6">
                <span className="text-4xl font-extrabold tracking-tight text-amber-500">{currentOverview.stats.notified}</span>
              </CardContent>
              <div className="h-1.5 w-full bg-amber-500/20" />
            </Card>

            {/* Stat Contacted */}
            <Card className="flex flex-col justify-between overflow-hidden shadow-sm border border-border/60 hover:border-border transition-all duration-300">
              <CardHeader className="pb-2">
                <CardDescription className="text-xs font-semibold uppercase tracking-wider text-emerald-500">Kontaktiert</CardDescription>
              </CardHeader>
              <CardContent className="pb-6">
                <span className="text-4xl font-extrabold tracking-tight text-emerald-500">{currentOverview.stats.contacted}</span>
              </CardContent>
              <div className="h-1.5 w-full bg-emerald-500/20" />
            </Card>
          </div>
        </div>

        {/* Search Profiles Card */}
        <Card className="shadow-sm border border-border/60">
          <CardHeader className="flex flex-row items-center justify-between pb-4">
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
                        value={newProfile.category}
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
                                ? "bg-emerald-500/10 text-emerald-500 border border-emerald-500/20"
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
                              <><Pause className="h-3.5 w-3.5 text-amber-500" /> Pause</>
                            ) : (
                              <><Play className="h-3.5 w-3.5 text-emerald-500" /> Aktiv</>
                            )}
                          </Button>
                          <Button
                            variant="ghost"
                            size="icon"
                            onClick={() => deleteProfile(p.id)}
                            className="h-8 w-8 hover:bg-destructive/10 hover:text-destructive text-muted-foreground"
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

        {/* Listings / Apartments Card */}
        <Card className="shadow-sm border border-border/60">
          <CardHeader className="flex flex-row items-center justify-between pb-4">
            <div>
              <CardTitle className="text-md font-bold tracking-tight">Gefundene Wohnungen</CardTitle>
              <CardDescription className="text-xs">Übersicht der {listings.length} neuesten Immobilienfunde</CardDescription>
            </div>
            
            {/* Interactive Search Filter */}
            <div className="relative w-full max-w-[280px]">
              <Search className="absolute left-2.5 top-2.5 h-4 w-4 text-muted-foreground" />
              <Input
                placeholder="Filtern..."
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                className="pl-9 h-9"
              />
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
                          <div className="inline-flex gap-1.5 justify-end">
                            {l.notified && (
                              <Badge
                                variant="outline"
                                className="h-6 gap-1 bg-amber-500/10 text-amber-600 dark:text-amber-500 border-amber-500/20 text-[10px] font-bold px-2 rounded-full"
                              >
                                <BellRing className="h-3 w-3" /> benachrichtigt
                              </Badge>
                            )}
                            {l.contacted && (
                              <Badge
                                variant="outline"
                                className="h-6 gap-1 bg-emerald-500/10 text-emerald-600 dark:text-emerald-500 border-emerald-500/20 text-[10px] font-bold px-2 rounded-full"
                              >
                                <CheckCircle2 className="h-3 w-3" /> kontaktiert
                              </Badge>
                            )}
                            {!l.notified && !l.contacted && (
                              <span className="text-xs text-muted-foreground italic px-2">Kein Status</span>
                            )}
                          </div>
                        </TableCell>
                      </TableRow>
                    ))
                  ) : (
                    <TableRow>
                      <TableCell colSpan={6} className="h-24 text-center text-muted-foreground text-sm">
                        {searchQuery ? "Keine passenden Wohnungen gefunden." : "Noch keine Wohnungen gefunden."}
                      </TableCell>
                    </TableRow>
                  )}
                </TableBody>
              </Table>
            </div>
          </CardContent>
        </Card>
      </main>


    </div>
  )
}

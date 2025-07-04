# Cabinet\_Api\_definition.md

## Cabinet modul API-definíció (magyar változat)

Ez a dokumentum a Cabinet komponens programozói API-felületének leírását tartalmazza, fejlesztők és integrátorok számára, magyar nyelven.

### Alapvető interfész (Go nyelvű példa)

```go
// Cabinet API interfész
interface Cabinet {
    // Séma regisztráció és lekérdezés
    RegisterSchema(SchemaDef) error
    GetSchema(name string) (SchemaDef, error)

    // Plugin regisztráció és lekérdezés
    RegisterPlugin(PluginDescriptor) error
    GetValidator(name string) (PluginDescriptor, error)
    GetPlugin(name string) (PluginDescriptor, error)

    // Gráf regisztráció és lekérdezés
    RegisterGraph(ExecutionGraph) error
    GetGraph(name string) (ExecutionGraph, error)

    // Intent feldolgozás
    ResolveIntent(intent IntentDef) (ExecutionPlan, error)

    // Segédmetódusok
    ListSchemas() []string
    ListPlugins() []string
    ListGraphs() []string
}
```

### Kapcsolódó fő típusok

* **SchemaDef** – Séma szerkezetének leírása (név, verzió, mezők, metaadatok)
* **PluginDescriptor** – Plugin neve, típusa, bemeneti/kimeneti sémái, metaadatai
* **ExecutionGraph** – Végrehajtási gráf: csomópontok, élek, kapcsolódó sémák
* **ExecutionPlan** – Egy intent végrehajtásához szükséges lépések sorozata
* **ExecutionStep** – Egy végrehajtási terv lépése (plugin, input, output, státusz)

### Megjegyzések

* Minden API-hívásnak szálbiztosnak (thread-safe) kell lennie.
* A Cabinet modul implementációja lehet szimulált (mock – teszteléshez), memóriabeli (in-memory – fejlesztéshez), vagy tartós (perzisztens – éles környezethez).
* A hibakezelés egységes, jól dokumentált hibatípusokat használjon.

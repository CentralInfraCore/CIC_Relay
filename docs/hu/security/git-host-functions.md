# Git host függvények: capability-szerződés

A relay a WASM moduloknak típusos `git` host modult kínál. A modul soha nem kap shellt vagy útvonalat: egy **regisztrált repository** azonosítójával hivatkozik a repóra, és egy kis műveletkészletből kér egyet. Ez a dokumentum az operátor, a modulszerző és a relay közti szerződés.

## 1. Mit hívhat egy modul

| Függvény | Engedett | Megjegyzés |
|---|---|---|
| `git.status` | igen | a regisztrált repository munkafájának állapota |
| `git.fetch` | igen | a repository egy konfigurált remote-járól |
| `git.checkout` | igen | létező branch, tag vagy commit; a csak távoli branchnévből nem jön létre követő ág |
| `git.commit` | igen | pontosan a kérésben megnevezett útvonalak; a korábban mástól stage-elt változás nem kerül bele |
| `git.clone` | igen | csak a manifestben felsorolt pontos forrásról, a regisztrált helyre |
| `git.push` | **nem** | visszavonva, lásd az 5. szakaszt |

## 2. A capability manifest

A modul az aláírt `project.yaml` `capability_manifest.git` blokkjában deklarálja a hatókörét. Semmi nem következik implicit módon: a fel nem sorolt művelet, repository, remote, ref vagy clone-forrás elutasított.

```yaml
abi:
  imports:
    - module: git
      functions: [status, fetch, checkout]
capability_manifest:
  git:
    - repo: infra                 # egy pontos regisztrált repository-azonosító, joker nélkül
      ops: [status, fetch, checkout]
      remotes: [origin]           # fetch-hez kötelező: konfigurált remote NEVEK
      refs: ["main", "release/*"] # clone-hoz és checkout-hoz kötelező: pontos név vagy "prefix/*"
      # sources: ["https://example.org/infra.git"]   # clone-hoz kötelező: pontos URL-ek
```

Betöltéskor ellenőrzött (a megsértés a modul elutasítását jelenti):

- minden importált `git.*` függvényt le kell fednie egy manifest-műveletnek;
- az `ops` csak `clone`, `fetch`, `checkout`, `commit`, `status` lehet; **a `push` hiba**, és a `git.push` manifestben vagy az `abi.imports`-ban való deklarálása is hiba;
- a `fetch` `remotes`-t, a `clone` és a `checkout` `refs`-t, a `clone` `sources`-t igényel;
- a ref-minták csak branch- és tag-neveket jelölhetnek: a `refs/` alatt csak `refs/heads/` és `refs/tags/`, `HEAD` nem, puszta `*` nem.

**Deklarált és tényleges importok.** A betöltéskori importellenőrzés a *deklarált* importokat olvassa (az aláírt manifest `abi.imports`-át). A WASM bináris saját import szekcióját nem olvassa. Az a bináris, amely deklarálás nélkül importál `git.*` függvényt, ezért betöltéskor nem utasítódik el; a hívásai a lent leírt futásidejű védelmekbe ütköznek (a megfelelő grant nélküli modul hívása elutasított, a `git.push` pedig mindig elutasító csonk). A bináris tényleges importlistájának betöltéskori ellenőrzése külön, nyitott feladat.

Minden híváskor ellenőrzött: a hívó példány, a repository, a művelet, a remote, a ref vagy a forrás a fenti szabályokhoz képest. A nem ellenőrzött modul (nincs CICSourceCA-provenance, nincs bootstrapolt trust) egyáltalán nem kap git capabilityt.

### Identitás és életciklus

- A hívót az **instance identity** azonosítja, amelyet a futtatókörnyezet csatol a híváshoz: modulazonosító, a példány létrehozásakor használt artifact checksumja, példányonkénti sorszám és a létrehozáskor érvényes manifest digestje. A guest ezt nem adhatja meg és nem módosíthatja; a modul start section-je is ez alatt fut.
- A grant az ellenőrzött artifacthoz **és** ahhoz a manifesthez tartozik. Ugyanazon modulazonosító újratöltése előbb visszavonja a korábbi grantot; az a példány, amely más artifact vagy más manifest alatt indult, elutasított.
- Egy modulazonosító betöltései sorosítottak. A grant staged állapotban jön létre, az artifact eltárolódik, majd a grant pontosan arra az artifactra aktiválódik; a staged grant semmit nem engedélyez.

## 3. Amit a relay minden híváskor ellenőriz

- A **remote-oknak** konfiguráltnak kell lenniük a repositoryban. Az URL(ek), ahonnan a fetch olvas (az URL-átírás és a beépített konfiguráció alkalmazása után), `https://` vagy `ssh://` lehetnek (nyomtatható ASCII, szóköz nélkül, **jelszó nélkül az URL-ben**; a puszta felhasználónév megengedett, például az `ssh://git@host/...` URL `git@` része). Helyi útvonal vagy `file://` forrás csak operátor által engedélyezett opcióként létezik, amely élesben nincs bekötve.
- A **ref-ek** a git ref-névszabályait követik; a `commit` útvonalai relatívak, a repositoryn belüliek, soha nem a `.git` alattiak, és soha nem pathspec-mágiák.
- **Repository-konfiguráció** (minden, *már létező* regisztrált repositoryn végzett művelet előtt ellenőrzött: `status`, `fetch`, `checkout`, `commit`; a `clone` új repositoryt hoz létre üres célkönyvtárba, ezért előtte nincs mit auditálni, és minden későbbi művelet audit alá esik): a regisztrált repository csak azokat a kulcsokat tartalmazhatja, amelyeket a `git clone` ír, a remote `url`/`pushurl`/`fetch` kulcsait, a branch `remote`/`merge` kulcsait, a `user.name`/`user.email`-t és a SHA-256 `extensions.objectformat`-ot. Bármely más kulcs (futtatandó program, URL-átírás, include, worktree-konfiguráció, ...) minden műveletet meghiúsít az adott repositoryn. A repositoryt kell javítani, nem a relayt.
- **Maga a git** kikapcsolt hookokkal, csak `https` és `ssh` transporttal, literal pathspecekkel, kérdezés nélkül, **globális és rendszerkonfiguráció nélkül** fut (lásd lent a `CIC_GIT_GLOBAL_CONFIG`-ot). A gyerekfolyamat környezete allowlistes, nem a relayé.
- Az URL-ben lévő felhasználói információ maszkolva van mindenhol, ahol URL-t tartalmazható szöveg elhagyja a git réteget (hibák, a git saját kimenete, naplók); a validációs hibák az URL-t egyáltalán nem ismétlik.

## 4. Operátori konfiguráció

| Változó | Jelentés |
|---|---|
| `CIC_INFRA_REPO_PATH`, `CIC_GIT_SOURCE_LOCAL_PATH` | az `infra` és az `iac-config` repository regisztrálása |
| `CIC_GIT_GLOBAL_CONFIG` | opcionális, egy git-konfigurációs fájl abszolút útvonala, amelyet *globális* konfigurációként olvas (például egy credential helper). Ha nincs megadva: semmit nem olvas. Szabályos fájlnak kell lennie, amelyet csoport és mások nem írhatnak; különben a git műveletek elutasítottak. |

## 5. A push visszavonva

A `git.push` jelenleg nem adható. A guest push-hoz a pontos forrásobjektumot, a remote azonosságát, a pontos célreferenciát és annak elvárt korábbi állapotát egyetlen engedélyezett döntéshez kell kötni; a `git` parancssor mindkét végén név szerint illeszti a referenciákat, és ezt a garanciát nem adja. Amíg nincs megtervezett és bizonyított push-primitív:

- a manifest nem adhat `push`-t és a modul nem deklarálhatja importként (mindkettő betöltési hiba); az a bináris, amely deklarálás nélkül importálja, linkelődik, és az alábbi csonkba ütközik;
- a regiszter a `push`-t elutasítja, bármit mond a grant;
- a `git.push` host függvény megmarad, hogy az importáló modul linkelhető legyen, de tiltó csonk: mindig hibával válaszol és nem éri el a git réteget.

A push visszavezetése külön döntés és külön munkacsomag.

## 6. A feltételezések, amelyeket az operátornak tartania kell

- A regisztrált repository-könyvtárakat **nem írhatja senki, aki kevésbé megbízott, mint a relay hitelesített API-principaljai és az operátorok**. Ezek a principalok már most is futtathatnak parancsot a relay felhasználójaként; a modulok nem.
- A távoli git szerverek a relay hatókörén kívül vannak; a relay csak olvas róluk (`fetch`, `clone`).

## 7. Viselkedésváltozások

- Az a modul, amely egy `git.*` függvényt megfelelő manifest-szabály nélkül importál, betöltéskor elutasított.
- Git capabilityt csak ellenőrzött modul kap.
- A `checkout <név>` többé nem hoz létre helyi követő ágat a csak távoli névből.
- A 3. szakasz allowlistjén kívüli konfigurációjú repositoryk elutasítottak, amíg meg nem tisztítják őket.
- A globális git-konfiguráció nem olvasódik többé.

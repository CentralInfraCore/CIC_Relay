# CI Merkle DAG — szerződés (első verzió)

**Státusz:** első megvalósítás (`pkg/cidag`, `tools/cidag`), **additív**: egyetlen meglévő hash-t, gyökeret vagy kanonikus formát nem módosít. Az architekturális irány a `CI_MerkleDAG.md` dokumentum (megfogalmazott irány, nem állítás a teljes megvalósultságról); ez a szerződés annak a **legkisebb** részét rögzíti és valósítja meg, amelyet a meglévő CIC-kód már alátámaszt.

Kulcsszavak: MUST, MUST NOT, SHOULD (RFC 2119 értelemben).

## 1. Mit ad, és mit nem

Egy CI-folyamat minden lépéséhez **kanonikus objektum** (definíció, bemenetek, függőségek gyökerei, eredmény), ezekből a teljes folyamat **CI-gyökere**. Azonos sablon és azonos bizonyíték mindig azonos gyökeret ad; bármely releváns bemenet, eredmény vagy kapcsolat módosítása kimutathatóan megváltoztatja az érintett lépés és minden függője gyökerét, valamint a CI-gyökeret.

**Nem állítja**, hogy a lépés ténylegesen lefutott, vagy hogy az eredmény igaz: a gyökér **belső konzisztenciát és tartalmi integritást** képvisel. A végrehajtás hitelessége külön kérdés (a Relay későbbi, objektumonkénti környezeti megfigyelése, külső aláírás, időbizonyíték). A kompozíció (egy gyökér másik objektum függőségeként) az integritást viszi tovább, a hitelességet nem.

**Nem érint:** a tesztbizonyíték-cache Merkle-mechanizmusát (`cic-primitive-forms/tools/test-evidence`, `proof-plan`), a `pkg/merkle` `VerificationManifest` gyökerét, a `ProofTrace` láncát, a `pkg/obs` megfigyelési rekordjait, a release-envelope-ot és a commit-hitelesítést. Ezek külön mechanizmusok; a CI-gyökér és a build-gyökér összekapcsolása külön szerződés-döntés (nincs része ennek).

## 2. Mit használ újra, és mit vezet be

| Elem | Forrás | Státusz |
|---|---|---|
| Kanonikus bájtok | `pkg/canonicaljson.ToJSON` (CIC Canonical Object Encoding) | **újrahasznált**, változatlan |
| Verziózott domain-prefix + `\x00` | a `core/cabinet.ComputeChainHashV1` stílusa (`CICCHAINv1`) | **újrahasznált stílus**, új prefixek |
| `sha256:<64 hex>` digest-alak | release envelope 3 (`RELEASE-RECORD-CONTRACT`), engine A7 | **újrahasznált alak** |
| Lépés-állapotok | a Forms `record.py` `CONCLUSIONS` halmaza | **újrahasznált halmaz** |
| Szándékosan hibás ellenőrzők mintája | Forms `seeded.py` | **újrahasznált minta** |
| Lépésobjektum, sablon, CI-gyökér, ellenőrző | — | **új** (ez a szerződés) |

**Amit nem használ újra, és miért:** a `pkg/merkle.MerkleRoot`-ot. Mért tulajdonság (`golang:1.26.11`, a `merkle.go` változatlan másolatán): `root([a,b,c]) == root([a,b,c,c])`, mert páratlan szintnél az utolsó elemet duplikálja (`merkle.go:70`). A rögzített mezőkészletű `VerificationManifest`-nél ez nem kihasználható, **változó hosszúságú listára** (lépések, függőségek) viszont az. Ugyanott mért: a levél neve és értéke között csak egy `0x1f` van hosszelőtag nélkül, és az üres érték azonos a `∅` literállal. A meglévő gyökér szerződését nem módosítjuk.

## 3. A kanonikus forma: szűkített profil

A CIC Canonical Object Encoding a Go `encoding/json` escape-szabályait örökli (`<`, `>`, `&`, U+2028/9 átírása, nincs Unicode-normalizálás; engine `MATERIALIZATION-SPEC` A4). Hogy két független implementáció ne térhessen el, a lépésobjektum **profilja** olyan értékekre szorít, amelyekre az escape soha nem alkalmazódik:

* sztring: nyomtatható ASCII (`0x20`–`0x7e`) a `"`, `\`, `<`, `>`, `&` nélkül;
* szám: **egész**, `|n| ≤ 2^53−1`; lebegőpontos érték nincs;
* `true`, `false`, `null`, tömb, objektum; az objektum kulcsai ugyanezen sztring-profil szerint, **nyers UTF-8 bájtsorrendben** rendezve; nincs szóköz.

A profilon belül a bájtok azonosak a `pkg/canonicaljson.ToJSON`, a Python `json.dumps(sort_keys=True, separators=(",", ":"))` (a release-compiler formája) és a Forms `canon.py` (RFC 8785, I-JSON) kimenetével. Ez **mért** (34 érték, három implementáció, bájtazonos); a profilon kívül (nem ASCII kulcs, `<&>`) a három eltér, ezért a profil azok metszete, és a profilon kívüli értéket az írás elutasítja (`PROFILE`).

Az olvasó szigorú (`ParseStrict`): elutasítja az ismétlődő tagnevet, minden nem egész számot (`1.5`, `1e3`, `1.0`), az `int64`-en túli egészet, a `NaN`/`Infinity` konstanst, a dokumentum utáni adatot és a 64-nél mélyebb beágyazást (`PARSE`).

## 4. Objektumok

**Lépés** (`cic-ci-step/1`), pontosan ezekkel a tagokkal:

```json
{"contract":"cic-ci-step/1","dependencies":["sha256:…"],"inputs":{"név":"érték"},
 "result":{"digest":"sha256:…","status":"success"},"step":{"definition_digest":"sha256:…","id":"build"}}
```

* `step.id`, bemenet-név, workflow-azonosító: `[A-Za-z0-9][A-Za-z0-9._:/@+=-]{0,127}`.
* `inputs`: név → bool, egész (profil szerint) vagy 1–256 karakteres profil-sztring. A bemenet **általános fogalom**: egy forrás-, teszt-, konfiguráció- vagy másik objektum-digest ugyanaz az eset, mint egy paraméter; **nincs lépés- vagy teszttípus-specifikus szabály**. `null` bemenet nincs (elhagyás).
* `dependencies`: a `needs` lépések **gyökereinek** rendezett listája; ezek teszik a kapcsolatokat a hash részévé.
* `result.status`: `success`, `failure`, `cancelled`, `skipped`, `neutral`, `timed_out`, `action_required`, `stale`. `result.digest` **`null` pontosan akkor, ha a státusz `skipped` vagy `cancelled`** (nem keletkezett eredmény), különben `sha256:<64 hex>`. Az eredmény tartalma **a lépés saját szerződése**: ez a szerződés nem ír elő általános normalizálást.
* Lépés-gyökér: `sha256:` + hex(SHA-256(`"CICSTEPv1\x00"` ‖ kanonikus bájtok)).

**Sablon** (`cic-ci-template/1`): `{"contract","steps":{id:{"definition_digest","needs":[rendezett id-k]}},"workflow"}`, digestje `"CICTEMPLATEv1\x00"` prefixszel. A sablon a verziózott definíciókból **származtatott**, nem tárolt adat (`TemplateFromWorkflow`: egy GitHub Actions workflow `jobs` kulcsaiból és `needs`-éből; a definíció-digest a workflow-fájl nyers bájtjainak SHA-256-ja: durva, de nem függ YAML-szemantikától).

**CI-gyökér** (`cic-ci-root/1`): `{"contract","steps":{id:lépés-gyökér},"template_digest","workflow"}`, gyökere `"CICCIROOTv1\x00"` prefixszel.

**Aggregálás.** A gyökér **nem** a hash-ek összefűzése: a lépésobjektum a függők gyökerét hash-eli (a kapcsolatok a struktúra részei), a CI-gyökér az `id → gyökér` térképet és a sablon digestjét. A sorrend szemantikailag közömbös helyeken **rendezett** (kulcsok, `needs`, `dependencies`), tehát a deklarációs sorrend nem számít; az építési sorrend determinisztikus (Kahn, azonos id-sorrenddel). Hiányzó lépés (a sablon lépéséhez nincs bizonyíték: `EVIDENCE_MISSING`) és idegen lépés (`EVIDENCE_EXTRA`) elutasított; amelyik lépés nem futott, az **`skipped`**, nem hiányzó.

**Párhuzamos, sikertelen, kihagyott, dinamikus lépések.** Párhuzamos: sorrend nélküli testvérek. Sikertelen/megszakított/kihagyott: a státuszok (a függők lehetnek `skipped`). Dinamikus elágazás: egy döntési lépés, amelynek eredmény-digestje a választást azonosítja, a nem választott ág `skipped`; külön szabály nincs.

## 5. Ellenőrzés és hibaosztályok

`Verify` egy közzétett csomagot (`template`, `objects`, `step_roots`, `ci_root`) **újraszámol**: a közzétett objektumból csak az `inputs` és a `result` (a bizonyíték) olvasódik, a `contract`, `step`, `dependencies`, minden lépés-gyökér, a sablon-digest és a CI-gyökér újraszámolva összevetődik. Egy lépés módosítása a **záró** lépésnél a saját gyökerén, **korábbi** lépésnél egy ugrással lejjebb, a függő `dependencies` mezőjén bukik.

Hibaosztályok (a két implementáció és a vektorok azonosan nevezik): `PARSE`, `PROFILE`, `TEMPLATE_{SHAPE,EMPTY,ID,DIGEST,DUPLICATE_ID,NEED_UNKNOWN,NEED_DUPLICATE,CYCLE}`, `EVIDENCE_{MISSING,EXTRA,SHAPE}`, `INPUT_{NAME,VALUE}`, `RESULT_{SHAPE,STATUS,DIGEST}`, `BUNDLE_SHAPE`, `OBJECT_{SHAPE,CONTRACT_MISMATCH,STEP_MISMATCH,DEPENDENCIES_MISMATCH}`, `STEP_ROOT_MISMATCH`, `CI_ROOT_MISMATCH`. Az ellenőrzések sorrendje a szerződés része (az első elutasítás adja az osztályt).

## 6. Független ellenőrzés

* Elsődleges: `pkg/cidag` (Go). Független: `tests/test_tools/cidag_independent.py` (Python, csak standard könyvtár, **kódot nem oszt** a Go-val).
* Közös vektorok: `pkg/cidag/testdata/vectors.json` (6 pozitív, 53 negatív építési 16 hibaosztállyal, 30 negatív ellenőrzési 11 hibaosztállyal, 11 elutasított és 1 elfogadott olvasási eset, 13 kanonikus érték). A vektorok elvárásait a **Python orákulum** adja (`cidag_make_vectors.py`), a Go ellen mérve, nem fordítva.
* Az orákulumot a shell is megerősíti (lásd 7.).
* A vektorkészlet **bukni is tud**: a Python-implementációba ültetett 10 hiba (domain-prefix nélküli hash, fordított kulcssorrend, figyelmen kívül hagyott függőségek, nem összevetett függőség/gyökér/azonosság/szerződés, minden státusz elfogadva, idegen bizonyíték figyelmen kívül hagyva) mindegyikét legalább egy vektor elkapja (`test_cidag_independent.py`).

## 7. Kézzel újraszámolható példa (`single`)

Egy lépés (`build`), egy bemenet. A kanonikus lépésobjektum (egy sor):

```
{"contract":"cic-ci-step/1","dependencies":[],"inputs":{"source":"sha256:25a6634263c1b1f6fc4697a04e2b9904ea4b042a89af59dc93ec1f5d44848a26"},"result":{"digest":"sha256:762069bc07a6e1b5df123a5ae7bd91c10daa04694fbaa17fba0cd6a8dcce8f22","status":"success"},"step":{"definition_digest":"sha256:665f84cec0e7e19c0c59ba446b79595699b71f8c613c44b19cd32e84b12881fd","id":"build"}}
```

```sh
printf 'CICSTEPv1\0%s' "$OBJEKTUM" | sha256sum      # 1a91e268620d9eed69bf1e183cb2bc91f2046f427a6004c7f6be2bdad14dba4f
printf 'CICTEMPLATEv1\0%s' "$SABLON" | sha256sum    # 1f18bbeaac2d8c3917497e7b8577e2aff076673992339b6e4d4632f6ac8b7e12
printf 'CICCIROOTv1\0%s' "$GYOKER_OBJ" | sha256sum   # e32c9614f7d46d93c43629a2918deb191431e34d0f85e014da72ba9882554707
```

ahol `$SABLON` = `{"contract":"cic-ci-template/1","steps":{"build":{"definition_digest":"sha256:665f84ce…","needs":[]}},"workflow":"ci"}`, `$GYOKER_OBJ` = `{"contract":"cic-ci-root/1","steps":{"build":"sha256:1a91e268…"},"template_digest":"sha256:1f18bbea…","workflow":"ci"}`. A három érték a `coreutils sha256sum` eredménye, a Python orákulumtól függetlenül.

## 8. Használat

```sh
go run ./tools/cidag template .github/workflows/verify.yml     # sablon egy workflow-ból
go run ./tools/cidag build   < bemenet.json                     # {"template","evidence"} → csomag
go run ./tools/cidag verify  < csomag.json                      # OK <gyökér> | REFUSED <osztály>: …
go test ./pkg/cidag ./tools/cidag
python -m pytest tests/test_tools/test_cidag_independent.py
python tests/test_tools/cidag_make_vectors.py                   # a vektorok újragenerálása (bájtra azonos)
```

Kilépési kód: 0 siker, 1 tartalmi elutasítás, 2 használati vagy I/O hiba. A CLI csak olvas és számol: nem ír fájlt, nem aláír, nem publikál.

## 9. Nyitott kérdések (nem döntöttük el)

1. A lépés eredményének tartalma lépéstípusonként (mi a `result.digest` egy teszt-, build-, release-lépésnél): ez a lépés szerződése.
2. A `definition_digest` finomsága: jelenleg a workflow-fájl nyers bájtjai (egy módosítás minden job digestjét változtatja).
3. A deklarált és a megfigyelt bizonyítási státusz **helye** (objektum, evidence vagy végrehajtási kontextus tulajdonsága): nem része ennek a verziónak; későbbi bevezetése új szerződés-verzió.
4. Unicode-normalizálás (az engine A4 megnevezett rése): a profil kerüli, nem oldja meg.
5. A `build_hash` / a `VerificationManifest` gyökerének viszonya ehhez a gyökérhez (a „Future: Merkle tree root over CI artifact layers" megjegyzések szerint): külön szerződés-döntés.
6. A forrás-horgony: a `provenance.source_commit` (release envelope 3) és a fa digestje; a commit-hitelesítés v4 a **szülőt nem köti** (mért, a hook szándékos döntése), a szülő-kapcsolat külön ellenőrzés tárgya.
7. A sablon generálása más CI-rendszerekből és a reusable workflow-k / mátrix-jobok kezelése.

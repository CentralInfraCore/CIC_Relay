# Forrás-provenance a séma-pipeline-ban

Ez a dokumentum azt írja le, hogyan rögzíti a `cic.schemas.pipeline`, hogy egy build
**melyik forrás-revízióból** készült, és ez hogyan jut el az aláírt artifactig és a
ProofTrace-ig.

## Miért számít

Két kérdés hasonlónak látszik, de nem az:

| Kérdés | Mi válaszol rá |
|---|---|
| „Ugyanaz a tartalom lett lefordítva?" | `build_hash` |
| „Az X repo Y commitjából jött?" | `verification_root` + `source_ref` |

A `build_hash` az `sha256(raw_artifact)` — szándékosan tiszta tartalom-digest. Két
különböző commit buildje **azonos** `build_hash`-t ad, ha a lefordított artifact
bájtra megegyezik, és ez így helyes.

A provenance-t a **verification manifest** hordozza, aminek a Merkle-gyökere a
`verification_root`. Ennek a `Source` blokkja a **vizsgált repót** kell leírja; a
`BuildEnv` és `Validation` blokkok pedig azt a relayt, amelyik a buildet végezte.
Ha ez a kettő összekeveredik, az attesztáció nem tud revíziókat megkülönböztetni.

## A mezők útja

```
cic.pipeline.start      feloldja a HEAD-et (vagy checkoutolja a `commit`-ot)
                        source_tree_digest-et számol a klónra
                        -> source_ref, source_tree_digest, builder_image_digest
        |
        |   test -> validate -> release        (copyMap viszi tovább)
        v
cic.source.assert       ellenőrzi az aláírt artifactot
                        -> továbbadja a provenance kulcsokat
        |
        v
cic.schema.build        előállítja a raw_artifact-ot
                        -> továbbadja a provenance kulcsokat
        |
        v
cic.artifact.sign       build_hash        = sha256(raw_artifact)
                        verification_root = a manifest Merkle-gyökere,
                                            a Source a folyamból töltve
                        -> visszaadja: source_ref, source_tree_digest
```

A `cic.source.assert` és a `cic.schema.build` a saját eredményét nulláról építi, ezért
mindkettő **explicit** adja tovább a provenance kulcsokat (`carryProvenance`). Ami
hiányzik, az hiányzóként marad — nem üres stringként —, mert az aláíró lépés ebből
a különbségből dönti el, hogy a futás egyáltalán hordozott-e repót.

## Commit rögzítése

```jsonc
POST /v1/schemas/pipeline
{
  "schema":   "postgresql",
  "version":  "v0.18.0",
  "repo_url": "https://github.com/org/repo",
  "commit":   "b0d1287abbb67a3ef79bfff27a5cc6b6a98ad5ee"
}
```

A `branch` és a `commit` közül legalább az egyik kötelező.

- **`commit` megadva** — teljes klón, majd `git checkout --detach <commit>`.
  A sekély, branch-hez kötött klón csak az adott branch csúcsát éri el, tehát nem
  tud kiszolgálni egy tetszőleges revíziót megnevező kérést.
- **Csak `branch`** — marad az olcsóbb `git clone --branch <branch> --depth 1`.

Ha van `commit`, az egyben a ProofTrace `source_digest`-je is lesz, így a lánc már a
futás elejétől a kért revízióhoz van horgonyozva.

## Visszaesés: repó nélküli futások

A `POST /v1/schema/compile` az `assert → build → sign` láncot futtatja, klónozás
nélkül. Nincs mit leírni repóként, ezért az aláíró lépés a relay saját build-idejű
konstansaira esik vissza (`BuildContext.SourceRef`, `SourceTreeDigest`, ldflag-eken
át injektálva). Erre a hívásra ez a helyes válasz: az egyetlen érintett forrás maga
a relay.

## Ellenőrzés

A `VerifyProofArtifact` figyelmeztet, ha a lánc nincs forrás-revízióhoz horgonyozva.
Az ellenőrzés az **üres** `source_digest`-re **és** az `"unknown"` sentinelre is
kiterjed, amit a relay akkor ír be, ha nincs megnevezhető revíziója — a kettő
ugyanazt jelenti, és ha csak az ürességet vizsgáljuk, egy valóban horgonyozatlan
trace csendben átmegy.

A figyelmeztetés nem érvényteleníti az artifactot: egy horgonyozatlan lánc is lehet
belsőleg konzisztens. Azt jelzi, hogy a lánc bizonyítja, *mit* buildeltek, de azt
nem, hogy *honnan jött*.

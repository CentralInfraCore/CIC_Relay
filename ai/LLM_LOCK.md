# LLM Lock (llm.lock)

Az `llm.lock` a **kanonikus definíciók** verziózárja. Célja, hogy egyértelmű legyen: mikor változott a modell/ember számára is
az értelmezés **alapja** (szerződés, szimbólumok, tesztelési elvek).

## Mikor kell bumpolni?
- `context/CONTRACT.md` változik (I/O, invariánsok, hibák)
- `context/SYMBOLS.md` változik (publikus szimbólumok, signature)
- `ai/PROMPTMAP.yaml` változik úgy, hogy a tesztelés/elfogadás logikája módosul
- `context/TESTING.md` vagy a GOLDEN struktúra **szemantikusan** változik
- `ai/LLM_RULES.md` jelentősen változik

**Nem** kell bumpolni kizárólag formátum/nyelvhelyesség miatti szerkesztésnél.

## Hogyan?
Használd a `scripts/bump_llm_lock.sh` segédet:

```bash
scripts/bump_llm_lock.sh "CONTRACT: input JSON pontosítás"   # opcionális megjegyzés
git add llm.lock
git commit -m "chore(llm.lock): bump"
```

A script növeli a `version` számot, frissíti a `last_changed` mezőt, és opcionálisan beír egy `note`-ot.

## Példa `llm.lock`
```yaml
version: 2
canonical_definitions: true
last_changed: 2025-09-03T10:15:00Z
note: "CONTRACT: input JSON pontosítás"
```

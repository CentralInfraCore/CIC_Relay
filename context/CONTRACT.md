# CONTRACT
Feladat: cabinet bemenetből (konfiguráció/séma) **canonical JSON** kimenetet előállítani.
Input: JSON (bemeneti graf/séma leírás).
Output: JSON (rendezett kulcsok, stabil formátum, newline a végén).
Invariánsok: determinisztikus, side‑effect free; nincs clock/locale/RNG.
Hibatér: olvasási/parszolási hiba → `ErrInput`.

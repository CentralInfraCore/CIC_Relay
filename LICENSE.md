# LICENSE

This project is licensed under the **● Minden működik. Összefoglaló a jelenlegi állapotról:
                                                                                                                                                                                                                           
---                                                                                                                                                                                                                      
DEV migration — kész

Új NLB IP: 158.179.57.146 (a régi 158.179.52.3 helyett, ami törlésre került)

Mi működik:
- Gateway API: 4 listener (HTTP, HTTPS k8spaynance.com, HTTPS paynance.hu, HTTPS solutron*)
- 15 HTTPRoute — mind csatolva és programozva
- HTTP routing: 301/404 (app-szintű válasz) ✓
- HTTPS routing SNI-vel: 301/404 ✓
- Service targetPort: javítva 443→80 azoknál amelyek mindkét portot exponálják

Nyitott problémák:
1. mssettings — CrashLoopBackOff, alkalmazásszintű crash
2. https-solutron listener — lejárt cert (2025-04-30), de a route csatolva van
3. 443NLB (158.179.59.21) — az NLB limit tele van (5/5), ha az old DEV kubespray cluster már nem kell, ezt lehetne törölni egy szabad slot-ért
4. DNS rekordokat a 158.179.57.146 IP-re kell átírni (ha volt)

*A solutron-tls-secret lejárt cert miatt a HTTPS solutron listener Invalid állapotban van — a cert megújítása szükséges.

✻ Churned for 22m 55s                                                                                                       ** license.

## 📌 Summary (non-legally binding)

This license includes the following terms:

- **Attribution (BY)** – You must give appropriate credit to the original authors.
- **NonCommercial (NC)** – You may not use the material for commercial purposes without explicit permission.
- **ShareAlike (SA)** – If you remix, transform, or build upon the material, you must distribute your contributions under the same license.

## ✅ What does this mean in practice?

- You may use, copy, modify, and share the contents of this project **for personal, educational, or research purposes**.
- You must always credit the original author when using or sharing it.
- You may not sell, commercialize, or integrate the material into proprietary systems without written consent.
- If you publish modified versions, they must also be licensed under CC BY-NC-SA 4.0.

## 📄 Full legal text

For the full legal license, visit:  
[https://creativecommons.org/licenses/by-nc-sa/4.0/legalcode](https://creativecommons.org/licenses/by-nc-sa/4.0/legalcode)

## © Copyright and Attribution

© 2025 Sinkó Gábor Zoltán / OpenIntentSign / CentralInfraCore  
All rights reserved, except as explicitly permitted by the license above.

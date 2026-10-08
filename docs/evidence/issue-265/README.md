# Issue265: combined agent-kind effect fixture

Date2026-10-09; stack based on fix/issue-210 c2805be. Fixture-only correction;
no shipped UI/runtime/protocol changes and no compatibility behavior.

The original fixture passes8/8 standalone but fails4 home/peer create/chooser
cases when #208 changes valid guidance from not running to not available.
The actual UI gates/selected-kind handling remain correct. The fixture now
requires the nonempty current droppedAgent output in the real DOM; exact
wording remains in the pure helper tests. Pending requests, latest entry/origin,
removed selection and no silent substitution assertions remain unchanged.

Node24/existingChromium151: parent #210 fixture passes8/8, combined runtime
fixture passes8/8. Parent frozen install/typecheck passes. In the combined
checkout, temporarily removing both rendered dropped props makes the four
home/peer create/chooser checks fail again; the mutation is restored in finally.
This proves the assertion still detects missing rendered guidance. No real
broker/provider/account/input was used; no browser installation occurred.
Frozen site install and Astro29-page/link build passed. Independent review
checked the nonempty actual-DOM assertion and retained state/request gates,
and independently reran all8parent scenarios successfully. Hosted checks
remain disabled by owner.

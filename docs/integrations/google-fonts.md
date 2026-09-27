# Google Fonts

Google Fonts supplies application typography through a browser CSS import. It is a presentation dependency, separate from Google Identity and transport data.

## Resource and inputs

[The stylesheet](../../frontend/src/style.css) imports:

```text
https://fonts.googleapis.com/css2?family=Plus+Jakarta+Sans:wght@400;500;600;700;800&display=swap
```

| Input | Meaning/use |
|---|---|
| `family=Plus+Jakarta+Sans` | Requested font family |
| `wght@400;500;600;700;800` | Requested weights |
| `display=swap` | Requested font-display policy |
| Returned CSS/font resources | Browser typography rendering |

The CSS font stack uses Plus Jakarta Sans with a sans-serif fallback. This is distinct from map-label fonts resolved by the OpenFreeMap style/MapLibre renderer. Font-resource URLs are determined by the returned CSS and can vary; no exhaustive current host/resource inventory is asserted here.

## Collection, lifecycle and missing data

The browser loads the CSS/font resources when rendering the frontend, with no backend polling, source token or dedicated application environment setting. Browser response/cache policy controls resource reuse. The shared transport-provider budget does not apply.

There are no normalized transport fields, identity joins, application SQL records or historical observations from this integration. The app does not persist returned font CSS/files in its database. If fonts are unavailable, the CSS fallback stack can render text; this does not indicate missing vehicle or schedule data.

Synthetic example: a failed external font download leaves text rendered using a fallback family while the API's transport data lifecycle is unchanged.

## Evidence

Implementation: [CSS import and font stack](../../frontend/src/style.css), [frontend stylesheet loading](../../frontend/src/main.tsx). This inventory comes from the implemented resource request; it does not assert a freshly verified upstream availability or licensing contract.

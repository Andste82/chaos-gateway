# UI prototype

Clickable design of the main Chaos Gateway screens (plan §2.17), with sample data.

| File | Screen |
|---|---|
| `Main.dc.html` | Overview |
| `Devices.dc.html` | Devices |
| `DeviceDetail.dc.html` | Device detail + Add fault dialog |
| `Rules.dc.html` | Rules & Faults + preview-and-apply drawer |
| `Profiles.dc.html` | Profiles + probe measurement |
| `Scenarios.dc.html` | Scenarios + run |
| `Networks.dc.html` | Networks + access matrix with commit-confirm |

The files are Design Component pages (`.dc.html`) from the Claude design canvas: HTML markup plus a small logic class per screen. They need that canvas runtime to render, so treat them here as reference for layout, copy, colors and interaction — the product UI is built in React (plan §3.7). `canvas.json` is the canvas layout.

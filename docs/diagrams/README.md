# README Diagrams

The README diagrams are rendered from the adjacent Archify data-flow
specifications. The checked-in PNGs are the static GitHub-friendly exports.

To regenerate a diagram with a local Archify checkout:

```bash
node /path/to/archify/bin/archify.mjs validate dataflow <diagram>.dataflow.json --quality showcase --json
node /path/to/archify/bin/archify.mjs deliver dataflow <diagram>.dataflow.json <diagram>.html --quality showcase --json
```

Render the delivered HTML in a browser and export the diagram area as PNG. Do
not edit the PNGs directly.

The comparison diagrams intentionally keep relationship labels outside the PNG
crop. Their endpoints and stage headings already name the relationship, while
visible label pills would interrupt the converging paths.

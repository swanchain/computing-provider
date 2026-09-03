import type { ModelEarnings } from '../types';

/**
 * Categorical colours for per-model series, shared by every chart that splits
 * by model so the same model is the same colour everywhere on the page.
 *
 * Four hues, not eight. Validated against this dashboard's surface (#0f172a)
 * for every *pair*, not just neighbouring ones — a stacked bar and a donut are
 * both read by comparing segments that do not touch. Eight hues failed that
 * badly: the previous blue and violet were ΔE 0.3 apart under deuteranopia,
 * indistinguishable to a red-green colourblind reader, and only 10.2 apart with
 * full colour vision. Four is the largest set that passes, so everything beyond
 * the top four folds into a neutral "Other" rather than being given a colour
 * that cannot be told from another.
 *
 * Green and yellow sit at ΔE 6.9 under protanopia, inside the band that is
 * permitted only alongside a second cue. Both charts carry that: a legend, text
 * labels on hover, and a gap between adjacent fills.
 */
export const SERIES_COLOURS = ['#3987e5', '#c98500', '#d55181', '#008300'] as const;

/**
 * Deliberately neutral: "Other" is a residue, not an identity competing for
 * attention. Neutrals are exempt from the chroma floor by intent — what matters
 * is that they clear 3:1 against the surface and stay well apart from each
 * other (ΔE 18.7), so neither reads as a colour and neither disappears.
 */
export const OTHER_COLOUR = '#94a3b8';
export const OTHER_LABEL = 'Other';

/** Earnings an interval could not assign to any model — a different thing from "Other". */
export const UNATTRIBUTED_COLOUR = '#5b6b82';
export const UNATTRIBUTED_LABEL = 'Unattributed';

/** How many models get a colour of their own before the rest fold into Other. */
export const MAX_COLOURED_MODELS = SERIES_COLOURS.length;

export interface ModelColourMap {
  /** Model ID to colour, for the models that earned a hue of their own. */
  colours: Map<string, string>;
  /** The coloured model IDs, highest lifetime earnings first — the legend order. */
  ordered: string[];
  /** Model IDs folded into Other. */
  other: Set<string>;
}

/**
 * Assign colours from lifetime per-model earnings.
 *
 * Deliberately keyed on lifetime totals rather than on whatever window a chart
 * is currently showing. Changing a chart's time range must not repaint the
 * models that survive the change — a reader who has learned that blue is one
 * model should not have to relearn it because they clicked "7 days".
 *
 * Models that earned nothing are still eligible for Other, so a model serving
 * unpriced traffic does not vanish from a chart that shows tokens.
 */
export function buildModelColours(models: ModelEarnings[] | undefined): ModelColourMap {
  const ranked = [...(models ?? [])].sort((a, b) => {
    if (b.total_usd !== a.total_usd) return b.total_usd - a.total_usd;
    // A stable tiebreak, so two models on equal earnings (commonly zero) do not
    // swap colours between renders.
    return a.model.localeCompare(b.model);
  });

  const colours = new Map<string, string>();
  const ordered: string[] = [];
  const other = new Set<string>();

  ranked.forEach((m, index) => {
    if (index < MAX_COLOURED_MODELS) {
      colours.set(m.model, SERIES_COLOURS[index]);
      ordered.push(m.model);
    } else {
      other.add(m.model);
    }
  });

  return { colours, ordered, other };
}

/** The colour for a model, falling back to the neutral Other. */
export function colourFor(map: ModelColourMap, model: string): string {
  return map.colours.get(model) ?? OTHER_COLOUR;
}

/** The label a model is shown under: itself, or Other when it was folded in. */
export function labelFor(map: ModelColourMap, model: string): string {
  return map.colours.has(model) ? model : OTHER_LABEL;
}

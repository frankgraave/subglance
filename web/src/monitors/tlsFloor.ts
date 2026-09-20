/**
 * The TLS floor's vocabulary: the values the select offers and what they mean.
 *
 * Apart from the component that renders them because `""` is a value with a
 * meaning — "this monitor has no opinion" — and three modules need to agree on
 * it: the add form must omit the field entirely for it, the edit form must not
 * turn it into a version on load, and the control must offer it as a choice.
 * A second spelling of it anywhere is a monitor pinned to a floor nobody set.
 */

/** No opinion. Not a version, and never coerced into the current default. */
export const TLS_FLOOR_UNSET = "";

/**
 * The options, in the order they are offered.
 *
 * The empty one is first because it is the state every monitor starts in: a
 * list that opened on 1.0 would suggest the floor is normally set.
 */
export const TLS_FLOOR_OPTIONS: readonly { value: string; label: string }[] = [
  {
    value: TLS_FLOOR_UNSET,
    label: "No opinion — follow the SubGlance default",
  },
  { value: "1.0", label: "TLS 1.0" },
  { value: "1.1", label: "TLS 1.1" },
  { value: "1.2", label: "TLS 1.2" },
  { value: "1.3", label: "TLS 1.3" },
];

import type { ComponentProps } from "react";

/**
 * The product's file chooser (DESIGN.md §8.11).
 *
 * A real `<input type="file">`: the OS file dialog, the keyboard, the label
 * association and the screen-reader announcement of the chosen file all stay
 * the browser's. `file-input.css` draws its button as the product's
 * `.button` through `::file-selector-button`, the one part of the control CSS
 * can reach. The text beside the button ("No file chosen", then the file's
 * name) is the browser's and cannot be restyled beyond its colour and type;
 * it is left as it is rather than hidden behind a label, because hiding the
 * input means rebuilding its focus ring and its announcement by hand.
 *
 * `tokens.test.ts` refuses a file input written anywhere else under `web/src`.
 */
export function FileInput({ className, ...rest }: Omit<ComponentProps<"input">, "type">) {
  return <input {...rest} type="file" className={className ? `file-input ${className}` : "file-input"} />;
}

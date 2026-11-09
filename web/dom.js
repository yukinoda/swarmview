export const $ = (id) => document.getElementById(id);

export function el(tag, className) {
  const element = document.createElement(tag);
  if (className) element.className = className;
  return element;
}

export function txt(element, value) {
  if (element.textContent !== value) element.textContent = value;
}

export function cls(element, value) {
  if (element.className !== value) element.className = value;
}

export function attr(element, name, value) {
  if (element.getAttribute(name) !== String(value)) element.setAttribute(name, value);
}

let nextID = 0;
export const uniqueID = (prefix) => prefix + nextID++;

// A click on a modal's ::backdrop targets the <dialog> itself, outside its box.
// Both the press and the click must land there, so a text selection dragged out of the panel never closes it.
// Native closedby="any" would do this, but WebKit (the macOS app) does not support it.
export function closeOnBackdrop(dialog, close) {
  const onBackdrop = (event) => {
    if (event.target !== dialog) return false;
    const rect = dialog.getBoundingClientRect();
    const insideX = event.clientX >= rect.left && event.clientX <= rect.right;
    const insideY = event.clientY >= rect.top && event.clientY <= rect.bottom;
    return !(insideX && insideY);
  };
  let pressedBackdrop = false;
  dialog.addEventListener('pointerdown', (event) => {
    pressedBackdrop = onBackdrop(event);
  });
  dialog.addEventListener('click', (event) => {
    const dismiss = pressedBackdrop && onBackdrop(event);
    pressedBackdrop = false;
    if (dismiss) close();
  });
}

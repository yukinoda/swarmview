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

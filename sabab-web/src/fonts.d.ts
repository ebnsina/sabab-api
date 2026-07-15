// Fontsource packages ship CSS, not TypeScript, so a bare side-effect import has
// no type declaration. This file has no import/export, so these declarations are
// truly ambient (a `declare module` inside a file that imports something becomes
// a module augmentation instead, which does not satisfy the side-effect import).
declare module '@fontsource-variable/mona-sans';
declare module '@fontsource-variable/geist-mono';

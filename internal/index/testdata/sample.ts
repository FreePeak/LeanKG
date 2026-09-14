export function ping(x: number): number {
  return double(x)
}

export const double = (x: number) => {
  const scaled = x * 2
  return scaled
}

export class Math {
  square(n: number): number {
    return n * n
  }
}

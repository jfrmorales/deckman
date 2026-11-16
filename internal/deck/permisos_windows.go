//go:build windows

package deck

// sinPermisosUnix: en Windows Go se inventa el modo (0666, y 0777 no sale
// nunca), asi que no dice nada de si un fichero es ejecutable en Linux. Ahi
// solo cuenta el contenido.
const sinPermisosUnix = true

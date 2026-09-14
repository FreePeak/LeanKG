namespace Quantum.Search {
    open Microsoft.Quantum.Canon;

    function IndexOfMarked(n : Int) : Int {
        return n - 1;
    }

    internal operation ApplyOracle(register : Qubit[]) : Unit is Adj + Ctl {
        X(register[0]);
    }
}

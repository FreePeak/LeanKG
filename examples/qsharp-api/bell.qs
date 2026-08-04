namespace Microsoft.Quantum.Samples {
    open Microsoft.Quantum.Intrinsic;
    open Microsoft.Quantum.Measurement;

    operation BellPair() : (Qubit, Qubit) {
        use qs = Qubit[2];
        H(qs[0]);
        CNOT(qs[0], qs[1]);
        MResetZ(qs[0]);
        return (qs[0], qs[1]);
    }

    operation EntanglePair() : Unit {
        let (a, b) = BellPair();
        X(a);
        CNOT(a, b);
    }
}
#[derive(Debug, Clone, PartialEq)]
pub enum DestType {
    Fragment,
    Composable,
    Activity,
    Dialog,
}

impl DestType {
    pub fn as_str(&self) -> &'static str {
        match self {
            DestType::Fragment => "fragment",
            DestType::Composable => "composable",
            DestType::Activity => "activity",
            DestType::Dialog => "dialog",
        }
    }
}

#[derive(Debug, Clone)]
pub struct NavDestination {
    pub id: String,
    pub class_name: Option<String>,
    pub dest_type: DestType,
    pub start_destination: bool,
}

#[derive(Debug, Clone)]
pub struct NavArg {
    pub name: String,
    pub arg_type: String,
    pub nullable: bool,
    pub default_value: Option<String>,
}

#[derive(Debug, Clone)]
pub struct NavAction {
    pub id: String,
    pub source_dest: String,
    pub target_dest: String,
    pub pop_up_to: Option<String>,
}

#[derive(Debug, Clone)]
pub struct NavDeepLink {
    pub uri: String,
    pub destination: String,
}
